package modelmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func id(n int) string { return fmt.Sprintf("%064x", n) }
func resources(resident, peak int) types.ModelResources {
	return types.ModelResources{GPURequired: peak > 0, ResidentMemoryMiB: resident, PeakMemoryMiB: peak, MaxConcurrentInferences: 1, IdleTimeoutSeconds: 1}
}

type fakeBackend struct {
	mu             sync.Mutex
	models         map[string]Model
	loaded         map[string]bool
	loads, unloads []string
	predict        func(context.Context, Model, types.PredictRequest) (types.PredictResponse, error)
	unload         func(context.Context, Model) error
	load           func(context.Context, Model) error
}

func newFake() *fakeBackend {
	return &fakeBackend{models: make(map[string]Model), loaded: make(map[string]bool)}
}
func (b *fakeBackend) add(n, resident, peak int) {
	b.models[id(n)] = Model{ContainerID: id(n), Name: fmt.Sprint(n), Resources: resources(resident, peak)}
}
func (b *fakeBackend) Prepare(_ context.Context, id string) (Model, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.models[id], nil
}
func (b *fakeBackend) Runtime(_ context.Context, m Model) (types.ModelRuntime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := types.RuntimeUnloaded
	if b.loaded[m.ContainerID] {
		state = types.RuntimeReady
	}
	return types.ModelRuntime{State: state, Loaded: b.loaded[m.ContainerID]}, nil
}
func (b *fakeBackend) Load(ctx context.Context, m Model) error {
	if b.load != nil {
		if err := b.load(ctx, m); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loaded[m.ContainerID] = true
	b.loads = append(b.loads, m.ContainerID)
	return nil
}
func (b *fakeBackend) Unload(ctx context.Context, m Model) error {
	b.mu.Lock()
	f := b.unload
	b.mu.Unlock()
	if f != nil {
		if err := f(ctx, m); err != nil {
			return err
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loaded[m.ContainerID] = false
	b.unloads = append(b.unloads, m.ContainerID)
	return nil
}
func (b *fakeBackend) Predict(ctx context.Context, m Model, r types.PredictRequest) (types.PredictResponse, error) {
	if b.predict != nil {
		return b.predict(ctx, m, r)
	}
	return types.PredictResponse{Success: true}, nil
}
func manager(t *testing.T, b Backend, budget int) *Manager {
	t.Helper()
	m, err := New(Config{Enabled: true, BudgetMiB: budget, QueueTimeout: time.Second, OperationTimeout: time.Second}, b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func request(m *Manager, n int) <-chan error { return requestCtx(m, context.Background(), n) }
func requestCtx(m *Manager, ctx context.Context, n int) <-chan error {
	done := make(chan error, 1)
	go func() { _, err := m.Predict(ctx, id(n), types.PredictRequest{}); done <- err }()
	return done
}
func receive(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish")
	}
}
func until(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func assertIdle(t *testing.T, m *Manager, reserved int) {
	t.Helper()
	s := m.Snapshot()
	if s.ReservedMiB != reserved {
		t.Fatalf("reserved=%d want=%d", s.ReservedMiB, reserved)
	}
	for _, e := range s.Models {
		if e.Active || e.QueueDepth != 0 {
			t.Fatalf("leaked ownership: %+v", e)
		}
	}
}
func age(m *Manager, n int, seconds int) {
	m.mu.Lock()
	m.models[id(n)].lastUsed = time.Now().Add(-time.Duration(seconds) * time.Second)
	m.mu.Unlock()
}

func TestFIFOAndBusyModelDoesNotBlockOtherModel(t *testing.T) {
	b := newFake()
	b.add(1, 10, 20)
	b.add(2, 10, 20)
	started := make(chan string, 8)
	release := make(chan struct{}, 8)
	b.predict = func(ctx context.Context, m Model, r types.PredictRequest) (types.PredictResponse, error) {
		started <- m.ContainerID
		select {
		case <-ctx.Done():
			return types.PredictResponse{}, ctx.Err()
		case <-release:
			return types.PredictResponse{Success: true}, nil
		}
	}
	m := manager(t, b, 100)
	one := request(m, 1)
	if <-started != id(1) {
		t.Fatal("wrong first model")
	}
	two := request(m, 1)
	until(t, func() bool { return m.Snapshot().Models[id(1)].QueueDepth == 1 })
	three := request(m, 1)
	until(t, func() bool { return m.Snapshot().Models[id(1)].QueueDepth == 2 })
	other := request(m, 2)
	select {
	case name := <-started:
		if name != id(2) {
			t.Fatal("same model ran concurrently")
		}
	case <-time.After(time.Second):
		t.Fatal("busy model blocked other queue")
	}
	// Release both concurrent models; same-model queued completions remain ordered.
	release <- struct{}{}
	release <- struct{}{}
	receive(t, one)
	receive(t, other)
	if <-started != id(1) {
		t.Fatal("wrong queued model")
	}
	release <- struct{}{}
	receive(t, two)
	if <-started != id(1) {
		t.Fatal("wrong final model")
	}
	release <- struct{}{}
	receive(t, three)
	assertIdle(t, m, 20)
	if len(b.loads) != 2 {
		t.Fatalf("resident model was reloaded: %v", b.loads)
	}
}

func TestPeakReservationBlocksUntilActiveDeltaReleased(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	b.add(2, 30, 80)
	started := make(chan string, 2)
	release := make(chan struct{}, 2)
	b.predict = func(ctx context.Context, m Model, _ types.PredictRequest) (types.PredictResponse, error) {
		started <- m.ContainerID
		select {
		case <-release:
			return types.PredictResponse{Success: true}, nil
		case <-ctx.Done():
			return types.PredictResponse{}, ctx.Err()
		}
	}
	m := manager(t, b, 110)
	one := request(m, 1)
	<-started
	two := request(m, 2)
	until(t, func() bool { return m.Snapshot().Models[id(2)].Active })
	if m.Snapshot().ReservedMiB != 80 {
		t.Fatal("unexpected peak accounting")
	}
	select {
	case <-started:
		t.Fatal("over-budget inference started")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	receive(t, one)
	<-started
	if m.Snapshot().ReservedMiB != 110 {
		t.Fatal("resident + active peak not reserved")
	}
	release <- struct{}{}
	receive(t, two)
	assertIdle(t, m, 60)
}

func TestLRUEvictionAndIdleEligibility(t *testing.T) {
	b := newFake()
	for n := 1; n <= 3; n++ {
		b.add(n, 30, 50)
	}
	m := manager(t, b, 100)
	receive(t, request(m, 1))
	receive(t, request(m, 2))
	age(m, 1, 5)
	age(m, 2, 3)
	receive(t, request(m, 3))
	assertIdle(t, m, 60)
	if len(b.unloads) != 1 || b.unloads[0] != id(1) {
		t.Fatalf("wrong eviction: %v", b.unloads)
	}
	if m.Snapshot().Models[id(1)].State != types.RuntimeUnloaded {
		t.Fatal("evicted model not unloaded")
	}
}

func TestIdleTimerWakesAdmissionWithoutAnotherRequest(t *testing.T) {
	b := newFake()
	b.add(1, 60, 60)
	b.add(2, 60, 60)
	m := manager(t, b, 100)
	receive(t, request(m, 1))
	m.mu.Lock()
	m.models[id(1)].lastUsed = time.Now().Add(-950 * time.Millisecond)
	m.mu.Unlock()
	receive(t, request(m, 2))
	if len(b.unloads) != 1 {
		t.Fatal("idle eligibility did not wake waiter")
	}
}

func TestIneligibleModelTimesOutWithoutEviction(t *testing.T) {
	b := newFake()
	b.add(1, 60, 60)
	b.add(2, 60, 60)
	m := manager(t, b, 100)
	m.config.QueueTimeout = 30 * time.Millisecond
	receive(t, request(m, 1))
	_, err := m.Predict(context.Background(), id(2), types.PredictRequest{})
	var admission *AdmissionTimeout
	if !errors.Is(err, ErrQueueTimeout) || !errors.As(err, &admission) || admission.HTTPStatus() != 503 || admission.RetryAfter() <= 0 {
		t.Fatalf("not retryable timeout: %v", err)
	}
	if len(b.unloads) != 0 {
		t.Fatal("evicted a model before its idle deadline")
	}
	assertIdle(t, m, 60)
}

func TestQueueCancellationRemovesWaiter(t *testing.T) {
	b := newFake()
	b.add(1, 10, 20)
	started := make(chan struct{})
	release := make(chan struct{})
	b.predict = func(context.Context, Model, types.PredictRequest) (types.PredictResponse, error) {
		close(started)
		<-release
		return types.PredictResponse{Success: true}, nil
	}
	m := manager(t, b, 100)
	one := request(m, 1)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	two := requestCtx(m, ctx, 1)
	until(t, func() bool { return m.Snapshot().Models[id(1)].QueueDepth == 1 })
	cancel()
	if err := <-two; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	receive(t, one)
	assertIdle(t, m, 10)
}

func TestCancellationKeepsPeakUntilCleanupConfirmed(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	started := make(chan struct{})
	cleaning := make(chan struct{})
	finishCleanup := make(chan struct{})
	b.predict = func(ctx context.Context, _ Model, _ types.PredictRequest) (types.PredictResponse, error) {
		close(started)
		<-ctx.Done()
		return types.PredictResponse{}, ctx.Err()
	}
	b.unload = func(ctx context.Context, _ Model) error {
		if ctx.Err() != nil {
			t.Error("cleanup inherited cancellation")
		}
		close(cleaning)
		<-finishCleanup
		return nil
	}
	m := manager(t, b, 100)
	ctx, cancel := context.WithCancel(context.Background())
	done := requestCtx(m, ctx, 1)
	<-started
	cancel()
	<-cleaning
	s := m.Snapshot()
	if s.ReservedMiB != 80 || !s.Models[id(1)].Active || s.Models[id(1)].State != types.RuntimeEvicting {
		t.Fatal("released live GPU reservation")
	}
	close(finishCleanup)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertIdle(t, m, 0)
}

func TestFailedCleanupIsQuarantinedAndRetryRecovers(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	b.predict = func(context.Context, Model, types.PredictRequest) (types.PredictResponse, error) {
		return types.PredictResponse{}, errors.New("prediction failed")
	}
	b.unload = func(context.Context, Model) error { return errors.New("unload unreachable") }
	m := manager(t, b, 100)
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	assertIdle(t, m, 80)
	if m.Snapshot().Models[id(1)].State != types.RuntimeError {
		t.Fatal("uncertain runtime not quarantined")
	}
	b.unload = nil
	b.predict = nil
	receive(t, request(m, 1))
	assertIdle(t, m, 30)
}

func TestPanicReleasesLeaseAndQueueOwnership(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	b.predict = func(context.Context, Model, types.PredictRequest) (types.PredictResponse, error) { panic("test panic") }
	m := manager(t, b, 100)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic not propagated")
			}
		}()
		_, _ = m.Predict(context.Background(), id(1), types.PredictRequest{})
	}()
	assertIdle(t, m, 0)
	b.predict = nil
	receive(t, request(m, 1))
}

func TestOperationTimeoutCleansUp(t *testing.T) {
	b := newFake()
	b.add(1, 10, 20)
	b.predict = func(ctx context.Context, _ Model, _ types.PredictRequest) (types.PredictResponse, error) {
		<-ctx.Done()
		return types.PredictResponse{}, ctx.Err()
	}
	m := manager(t, b, 100)
	m.config.OperationTimeout = 20 * time.Millisecond
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	assertIdle(t, m, 0)
}

func TestCPUFailureAlsoWaitsForCleanup(t *testing.T) {
	b := newFake()
	b.add(1, 0, 0)
	b.predict = func(context.Context, Model, types.PredictRequest) (types.PredictResponse, error) {
		return types.PredictResponse{Success: false}, nil
	}
	m := manager(t, b, 100)
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if err == nil {
		t.Fatal("false success accepted")
	}
	if len(b.unloads) != 1 {
		t.Fatal("CPU operation bypassed cleanup")
	}
	assertIdle(t, m, 0)
}

func TestCapacityMarginAndFullContainerIdentity(t *testing.T) {
	b := newFake()
	b.add(1, 50, 81)
	m := manager(t, b, 100)
	m.config.SafetyMarginMiB = 20
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if len(b.loads) != 0 {
		t.Fatal("oversized model loaded")
	}
	assertIdle(t, m, 0)
	_, err = m.Predict(context.Background(), "container-alias", types.PredictRequest{})
	if err == nil {
		t.Fatal("alias can create duplicate scheduling identity")
	}
}

func TestFirstTouchResidentIsNotSilentlyUnaccounted(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	b.loaded[id(1)] = true
	m := manager(t, b, 100)
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if !errors.Is(err, ErrUnmanagedResident) {
		t.Fatal(err)
	}
	assertIdle(t, m, 0)
	if len(b.unloads) != 1 {
		t.Fatal("unaccounted model not cleaned up")
	}
	receive(t, request(m, 1))
}

func TestConfigDisabledByDefaultAndValidatedWhenEnabled(t *testing.T) {
	for _, test := range []struct {
		name      string
		env       map[string]string
		wantError bool
	}{
		{"disabled", map[string]string{"GPU_MEMORY_BUDGET_MIB": "bad"}, false},
		{"invalid flag", map[string]string{"MODEL_MANAGER_ENABLED": "maybe"}, true},
		{"missing budget", map[string]string{"MODEL_MANAGER_ENABLED": "true"}, true},
		{"margin consumes budget", map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "100"}, true},
		{"invalid timeout", map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "81920", "MODEL_QUEUE_TIMEOUT_SECONDS": "0"}, true},
		{"enabled", map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "81920", "GPU_MEMORY_SAFETY_MARGIN_MIB": "4096"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := ConfigFromEnv(func(k string) string { return test.env[k] })
			if (err != nil) != test.wantError {
				t.Fatalf("config=%+v err=%v", c, err)
			}
		})
	}
	b := newFake()
	m := manager(t, b, 100)
	m.config.Enabled = false
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}

func TestLoadFailureCleansPotentialPartialAllocation(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	b.load = func(context.Context, Model) error { return errors.New("partial load failed") }
	m := manager(t, b, 100)
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if err == nil {
		t.Fatal("expected load failure")
	}
	if len(b.unloads) != 1 {
		t.Fatal("partial load was not cleaned up")
	}
	assertIdle(t, m, 0)
}

func TestFailedEvictionDoesNotAdmitOverBudgetModel(t *testing.T) {
	b := newFake()
	b.add(1, 60, 60)
	b.add(2, 60, 60)
	m := manager(t, b, 100)
	receive(t, request(m, 1))
	age(m, 1, 3)
	b.unload = func(context.Context, Model) error { return errors.New("eviction failed") }
	_, err := m.Predict(context.Background(), id(2), types.PredictRequest{})
	if err == nil {
		t.Fatal("expected eviction failure")
	}
	assertIdle(t, m, 60)
	if m.Snapshot().Models[id(1)].State != types.RuntimeError {
		t.Fatal("failed eviction not quarantined")
	}
	if len(b.loads) != 1 {
		t.Fatal("second model loaded without capacity")
	}
}

type runtimeFailureBackend struct{ *fakeBackend }

func (b runtimeFailureBackend) Runtime(context.Context, Model) (types.ModelRuntime, error) {
	return types.ModelRuntime{}, errors.New("runtime unreachable")
}
func TestUnreachableFirstTouchRuntimeRetainsConservativeReservation(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	b.unload = func(context.Context, Model) error { return errors.New("cleanup unreachable") }
	m := manager(t, runtimeFailureBackend{b}, 100)
	_, err := m.Predict(context.Background(), id(1), types.PredictRequest{})
	if err == nil {
		t.Fatal("expected unreachable runtime")
	}
	assertIdle(t, m, 80)
}

func TestOpaqueProviderErrorPreservesCancellationCause(t *testing.T) {
	b := newFake()
	b.add(1, 30, 80)
	started := make(chan struct{})
	b.predict = func(ctx context.Context, _ Model, _ types.PredictRequest) (types.PredictResponse, error) {
		close(started)
		<-ctx.Done()
		return types.PredictResponse{}, errors.New("DOCKER_INFERENCE_ERROR")
	}
	m := manager(t, b, 100)
	ctx, cancel := context.WithCancel(context.Background())
	done := requestCtx(m, ctx, 1)
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("provider masked cancellation: %v", err)
	}
	assertIdle(t, m, 0)
	if len(b.unloads) != 1 {
		t.Fatal("cancelled inference was not cleaned up")
	}
}
