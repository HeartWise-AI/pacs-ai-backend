package modelmanager

import (
	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	"context"
	"errors"
	"testing"
	"time"
)

const gpuA = "GPU-999e1fca-539d-fb99-2531-ad97b91da026"
const gpuB = "GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7"

func multiManager(t *testing.T, b *fakeBackend) *Manager {
	t.Helper()
	m, err := New(Config{Enabled: true, GPUBudgetsMiB: map[string]int{gpuA: 100, gpuB: 100}, QueueTimeout: time.Second, OperationTimeout: time.Second}, b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assign(b *fakeBackend, n int, uuid string) {
	model := b.models[id(n)]
	model.GPUUUID = uuid
	b.models[id(n)] = model
}

func TestPerGPUCapacityCannotBorrowFromIdleDevice(t *testing.T) {
	b := newFake()
	b.add(1, 20, 120)
	assign(b, 1, gpuA)
	m := multiManager(t, b)
	if _, err := m.Predict(context.Background(), id(1), api.PredictRequest{}); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if len(b.loads) != 0 {
		t.Fatal("loaded a model exceeding one device's capacity")
	}
}

func TestPerGPUReconciliationAndPlacement(t *testing.T) {
	b := newFake()
	for n, gpu := range []string{gpuA, gpuB} {
		b.add(n+1, 70, 80)
		assign(b, n+1, gpu)
		b.loaded[id(n+1)] = true
	}
	m := multiManager(t, b)
	if err := m.Reconcile(context.Background(), []string{id(1), id(2)}); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.GPUs[gpuA].ReservedMiB != 70 || s.GPUs[gpuB].AvailableMiB != 30 {
		t.Fatal(s)
	}
	assign(b, 2, gpuA)
	if err := m.Reconcile(context.Background(), []string{id(1), id(2)}); err == nil {
		t.Fatal("accepted overbooking on one GPU")
	}
	assign(b, 2, "")
	if err := m.Reconcile(context.Background(), []string{id(2)}); err == nil {
		t.Fatal("accepted ambiguous resident placement")
	}
}

func TestBusyGPUDoesNotBlockOtherDevice(t *testing.T) {
	b := newFake()
	for n, gpu := range []string{gpuA, gpuA, gpuB} {
		b.add(n+1, 60, 80)
		assign(b, n+1, gpu)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	b.predict = func(ctx context.Context, model Model, _ api.PredictRequest) (api.PredictResponse, error) {
		if model.ContainerID == id(1) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return api.PredictResponse{}, ctx.Err()
			}
		}
		return api.PredictResponse{Success: true}, nil
	}
	m := multiManager(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.Predict(ctx, id(1), api.PredictRequest{}); done <- err }()
	<-started
	waitCtx, stopWait := context.WithCancel(ctx)
	waitDone := make(chan error, 1)
	go func() { _, err := m.Predict(waitCtx, id(2), api.PredictRequest{}); waitDone <- err }()
	deadline := time.After(time.Second)
	for !m.Snapshot().Models[id(2)].Active || m.Snapshot().Models[id(2)].GPUUUID != gpuA {
		select {
		case <-deadline:
			t.Fatal("second request did not queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := m.Predict(ctx, id(3), api.PredictRequest{}); err != nil {
		t.Fatal("free GPU blocked:", err)
	}
	stopWait()
	<-waitDone
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMultiGPUConfig(t *testing.T) {
	env := map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGETS_MIB": `{"` + gpuA + `":24564,"` + gpuB + `":24576}`, "MODEL_IDLE_SWEEP_SECONDS": "30"}
	c, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil || len(c.GPUBudgetsMiB) != 2 || c.IdleSweepInterval != 30*time.Second {
		t.Fatal(c, err)
	}
	env["GPU_MEMORY_BUDGETS_MIB"] = `{"0":24576}`
	if _, err := ConfigFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("accepted CUDA ordinal")
	}
}

func TestWarmWaitersLeaveSameGPUResidentsEvictable(t *testing.T) {
	b := newFake()
	for n, gpu := range []string{gpuA, gpuA, gpuB} {
		b.add(n+1, 40, 70)
		assign(b, n+1, gpu)
		b.loaded[id(n+1)] = true
	}
	m := multiManager(t, b)
	m.config.QueueTimeout = 3 * time.Second
	if err := m.Reconcile(context.Background(), []string{id(1), id(2), id(3)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := requestCtx(m, ctx, 1)
	until(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.models[id(1)].active && m.admitting == nil
	})
	second := requestCtx(m, ctx, 2)
	until(t, func() bool {
		s := m.Snapshot().Models[id(2)]
		return s.QueueDepth == 1 || s.Active
	})
	if s := m.Snapshot().Models[id(2)]; s.Active {
		t.Fatal("second warm waiter pinned the resident needed for eviction")
	}
	// A queued warm request on A must not delay a warm request on B.
	receive(t, requestCtx(m, ctx, 3))
	m.mu.Lock()
	m.models[id(2)].lastUsed = time.Now().Add(-time.Hour)
	m.wakeLocked()
	m.mu.Unlock()
	receive(t, first)
	// Let the second request reclaim the first model after it becomes idle.
	m.mu.Lock()
	m.models[id(1)].lastUsed = time.Now().Add(-time.Hour)
	m.wakeLocked()
	m.mu.Unlock()
	receive(t, second)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.unloads) != 2 {
		t.Fatal("warm requests did not make progress through idle eviction", b.unloads)
	}
}

func TestLegacyReconciliationRetainsSingleSharedBudget(t *testing.T) {
	b := newFake()
	for n, gpu := range []string{gpuA, gpuB} {
		b.add(n+1, 70, 80)
		assign(b, n+1, gpu)
		b.loaded[id(n+1)] = true
	}
	m := manager(t, b, 100)
	if err := m.Reconcile(context.Background(), []string{id(1), id(2)}); err == nil {
		t.Fatal("legacy scalar budget was split implicitly")
	}
}

func TestPressureEvictionDoesNotUnloadAnotherGPU(t *testing.T) {
	b := newFake()
	b.add(1, 60, 80)
	assign(b, 1, gpuA)
	b.add(2, 60, 80)
	assign(b, 2, gpuB)
	b.add(3, 20, 60)
	assign(b, 3, gpuA)
	m := multiManager(t, b)
	m.models[id(1)] = &entry{model: b.models[id(1)], reserved: 80, state: api.RuntimeBusy, active: true, loaded: true}
	m.models[id(2)] = &entry{model: b.models[id(2)], reserved: 60, state: api.RuntimeReady, loaded: true, lastUsed: time.Now().Add(-time.Hour)}
	target := &entry{model: b.models[id(3)], active: true}
	m.models[id(3)] = target
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.reserve(ctx, target); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(b.unloads) != 0 {
		t.Fatal("evicted a model on the wrong GPU", b.unloads)
	}
}
