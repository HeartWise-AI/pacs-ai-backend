package modelmanager

import (
	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdleEvictionSkipsActiveQueuedAndDeploymentModels(t *testing.T) {
	b := newFake()
	m := multiManager(t, b)
	for n := 1; n <= 5; n++ {
		b.add(n, 20, 50)
		assign(b, n, gpuA)
		b.loaded[id(n)] = true
		m.models[id(n)] = &entry{model: b.models[id(n)], state: api.RuntimeReady, loaded: true, reserved: 20, lastUsed: time.Now().Add(-time.Hour)}
	}
	m.models[id(2)].active = true
	m.models[id(3)].blocked = true
	m.queue = []*waiter{{id: id(4)}}
	m.models[id(5)].lastUsed = time.Now()
	m.evictIdle(context.Background())
	if len(b.unloads) != 1 || b.unloads[0] != id(1) {
		t.Fatal("unloaded active, queued, blocked, or recently used model:", b.unloads)
	}
	if s := m.Snapshot(); s.Models[id(1)].State != api.RuntimeUnloaded || s.GPUs[gpuA].ReservedMiB != 80 {
		t.Fatal(s)
	}
}

func TestIdleEvictionFailureKeepsReservation(t *testing.T) {
	b := newFake()
	b.add(1, 20, 50)
	assign(b, 1, gpuA)
	b.loaded[id(1)] = true
	b.unload = func(context.Context, Model) error { return errors.New("process still running") }
	m := multiManager(t, b)
	m.models[id(1)] = &entry{model: b.models[id(1)], state: api.RuntimeReady, loaded: true, reserved: 20, lastUsed: time.Now().Add(-time.Hour)}
	m.evictIdle(context.Background())
	if s := m.Snapshot(); s.Models[id(1)].State != api.RuntimeError || s.GPUs[gpuA].ReservedMiB != 20 {
		t.Fatal(s)
	}
}

func TestIdleModelReloadsOnNextAutomaticOrManualRequest(t *testing.T) {
	b := newFake()
	b.add(1, 20, 50)
	assign(b, 1, gpuA)
	m := multiManager(t, b)
	if _, err := m.Predict(context.Background(), id(1), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.models[id(1)].lastUsed = time.Now().Add(-time.Hour)
	m.mu.Unlock()
	m.evictIdle(context.Background())
	if _, err := m.Predict(context.Background(), id(1), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(b.loads) != 2 || len(b.unloads) != 1 {
		t.Fatal(b.loads, b.unloads)
	}
}
