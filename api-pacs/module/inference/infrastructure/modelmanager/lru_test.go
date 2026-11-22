package modelmanager

import (
	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	"context"
	"errors"
	"testing"
	"time"
)

func lruManager(t *testing.T, b *fakeBackend, policy string) *Manager {
	t.Helper()
	m, err := New(Config{Enabled: true, BudgetMiB: 100, QueueTimeout: time.Second, OperationTimeout: time.Second, EvictionPolicy: policy}, b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// addResident registers a model that keeps its whole peak while loaded and
// stays resident for an hour unless something evicts it.
func addResident(b *fakeBackend, n int) {
	b.add(n, 50, 50)
	model := b.models[id(n)]
	model.Resources.IdleTimeoutSeconds = 3600
	b.models[id(n)] = model
}

func TestOnDemandPolicyEvictsLeastRecentlyUsedIdleModel(t *testing.T) {
	b := newFake()
	for n := 1; n <= 3; n++ {
		addResident(b, n)
	}
	m := lruManager(t, b, EvictionLRUOnDemand)
	receive(t, request(m, 1))
	receive(t, request(m, 2))
	// The GPU is full of models that answered; a third must not wait an hour.
	receive(t, request(m, 3))
	if len(b.unloads) != 1 || b.unloads[0] != id(1) {
		t.Fatal("expected the least recently used model to be evicted:", b.unloads)
	}
}

func TestIdleTimeoutPolicyStillWaitsForTheTimeout(t *testing.T) {
	b := newFake()
	for n := 1; n <= 3; n++ {
		addResident(b, n)
	}
	m := lruManager(t, b, EvictionIdleTimeout)
	receive(t, request(m, 1))
	receive(t, request(m, 2))
	select {
	case err := <-request(m, 3):
		if err == nil {
			t.Fatal("a resident model was evicted before its idle timeout")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request neither ran nor timed out")
	}
	if len(b.unloads) != 0 {
		t.Fatal(b.unloads)
	}
}

func TestOnDemandPolicyKeepsAModelForItsMinimumResidency(t *testing.T) {
	b := newFake()
	for n := 1; n <= 3; n++ {
		addResident(b, n)
	}
	m, err := New(Config{Enabled: true, BudgetMiB: 100, QueueTimeout: 200 * time.Millisecond, OperationTimeout: time.Second, EvictionPolicy: EvictionLRUOnDemand, MinResidency: time.Hour}, b)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, request(m, 1))
	receive(t, request(m, 2))
	if err = <-request(m, 3); err == nil {
		t.Fatal("evicted a model inside its minimum residency")
	}
	if len(b.unloads) != 0 {
		t.Fatal(b.unloads)
	}
}

func TestFailedEvictionTriesTheNextVictim(t *testing.T) {
	b := newFake()
	for n := 1; n <= 3; n++ {
		addResident(b, n)
	}
	b.unload = func(_ context.Context, model Model) error {
		if model.ContainerID == id(1) {
			return errors.New("model runtime HTTP status 502")
		}
		return nil
	}
	m := lruManager(t, b, EvictionLRUOnDemand)
	receive(t, request(m, 1))
	receive(t, request(m, 2))
	receive(t, request(m, 3))
	s := m.Snapshot()
	if s.Models[id(1)].State != api.RuntimeError || s.Models[id(1)].ReservedMiB != 50 {
		t.Fatal("the failed eviction must stay quarantined:", s.Models[id(1)])
	}
	if len(b.unloads) != 1 || b.unloads[0] != id(2) {
		t.Fatal("expected the second model to be evicted instead:", b.unloads)
	}
}

func TestSweepReleasesAQuarantinedReservationOnceUnloadSucceeds(t *testing.T) {
	b := newFake()
	b.add(1, 20, 50)
	assign(b, 1, gpuA)
	fail := true
	b.unload = func(context.Context, Model) error {
		if fail {
			return errors.New("process still restarting")
		}
		return nil
	}
	m := multiManager(t, b)
	m.models[id(1)] = &entry{model: b.models[id(1)], state: api.RuntimeError, loaded: true, reserved: 20, lastUsed: time.Now().Add(-time.Hour)}
	m.evictIdle(context.Background())
	if s := m.Snapshot(); s.Models[id(1)].State != api.RuntimeError || s.GPUs[gpuA].ReservedMiB != 20 {
		t.Fatal("a failed recovery must keep the reservation:", s)
	}
	fail = false
	m.evictIdle(context.Background())
	if s := m.Snapshot(); s.GPUs[gpuA].ReservedMiB != 20 {
		t.Fatal("recovery must back off after a failure:", s)
	}
	m.mu.Lock()
	m.models[id(1)].recoverAfter = time.Time{}
	m.mu.Unlock()
	m.evictIdle(context.Background())
	if s := m.Snapshot(); s.Models[id(1)].State != api.RuntimeUnloaded || s.GPUs[gpuA].ReservedMiB != 0 {
		t.Fatal("the quarantined reservation was not released:", s)
	}
}
