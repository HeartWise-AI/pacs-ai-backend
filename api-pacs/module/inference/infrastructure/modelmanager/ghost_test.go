package modelmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	dockererrdefs "github.com/containerd/errdefs"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
)

// ghostFixture is a model loaded on GPU A whose reservation the manager holds.
func ghostFixture(t *testing.T) (*fakeBackend, *Manager) {
	t.Helper()
	b := newFake()
	b.add(1, 20, 80)
	assign(b, 1, gpuA)
	b.loaded[id(1)] = true
	m := multiManager(t, b)
	m.models[id(1)] = &entry{model: b.models[id(1)], state: api.RuntimeReady, loaded: true, reserved: 20, lastUsed: time.Now()}
	return b, m
}

// replaced simulates a container swapped outside the gateway: the old one is
// stopped and renamed, a new container takes its name.
func replaced(b *fakeBackend, old, replacement int, loaded bool) {
	model := b.models[id(old)]
	model.ContainerID = id(replacement)
	b.models[id(replacement)] = model
	b.loaded[id(replacement)] = loaded
	b.loaded[id(old)] = false
	b.presence = map[string]ContainerPresence{id(old): {Exists: true, Stopped: true, Name: model.Name + "-pre-swap", ReplacementID: id(replacement)}}
}

func TestGhostReservationIsDroppedAndTheReplacementAdoptedByName(t *testing.T) {
	b, m := ghostFixture(t)
	replaced(b, 1, 9, true)
	m.evictIdle(context.Background())

	snapshot := m.Snapshot()
	old := snapshot.Models[id(1)]
	if old.ReservedMiB != 0 || old.Loaded || old.State != api.RuntimeUnloaded || old.Active {
		t.Fatalf("the replaced container kept its reservation: %+v", old)
	}
	adopted, ok := snapshot.Models[id(9)]
	if !ok || adopted.ReservedMiB != 20 || !adopted.Loaded || adopted.State != api.RuntimeReady || adopted.GPUUUID != gpuA {
		t.Fatalf("the replacement was not adopted with its real state: %+v (known=%v)", adopted, ok)
	}
	if snapshot.GPUs[gpuA].ReservedMiB != 20 {
		t.Fatalf("the device is reserved twice or not at all: %+v", snapshot.GPUs[gpuA])
	}
	if len(b.unloads) != 0 {
		t.Fatalf("a container was unloaded through the old container's name: %v", b.unloads)
	}

	// The adopted container is an ordinary model from now on.
	if _, err := m.Predict(context.Background(), id(9), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	// A second sweep finds nothing to do.
	if dropped := m.dropGhostReservations(context.Background()); dropped != 0 {
		t.Fatalf("second sweep dropped %d reservations", dropped)
	}
}

func TestGhostReservationOfAnUnloadedReplacementReservesNothing(t *testing.T) {
	b, m := ghostFixture(t)
	replaced(b, 1, 9, false)
	if dropped := m.dropGhostReservations(context.Background()); dropped != 1 {
		t.Fatalf("dropped=%d", dropped)
	}
	snapshot := m.Snapshot()
	if adopted, ok := snapshot.Models[id(9)]; !ok || adopted.ReservedMiB != 0 || adopted.State != api.RuntimeUnloaded {
		t.Fatalf("unexpected adoption: %+v (known=%v)", adopted, ok)
	}
	if snapshot.GPUs[gpuA].ReservedMiB != 0 {
		t.Fatalf("memory is still reserved: %+v", snapshot.GPUs[gpuA])
	}
}

func TestGhostReservationOfARemovedContainerIsDroppedWithoutAdoption(t *testing.T) {
	b, m := ghostFixture(t)
	b.presence = map[string]ContainerPresence{id(1): {}} // removed, and nothing took its name
	m.models[id(1)].state = api.RuntimeError             // an earlier unload through the stale name had failed
	m.models[id(1)].recoverAfter = time.Now().Add(time.Hour)
	m.evictIdle(context.Background())
	snapshot := m.Snapshot()
	if got := snapshot.Models[id(1)]; got.ReservedMiB != 0 || got.Loaded || got.State != api.RuntimeError {
		t.Fatalf("a removed container kept its reservation: %+v", got)
	}
	if len(snapshot.Models) != 1 || len(b.unloads) != 0 {
		t.Fatalf("unexpected inventory %v or unloads %v", snapshot.Models, b.unloads)
	}
}

func TestReservationIsKeptWhenTheContainerExistsOrDockerDoesNotAnswer(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		_, m := ghostFixture(t)
		if dropped := m.dropGhostReservations(context.Background()); dropped != 0 || m.Snapshot().Models[id(1)].ReservedMiB != 20 {
			t.Fatalf("a running container lost its reservation (dropped=%d)", dropped)
		}
	})
	t.Run("docker error", func(t *testing.T) {
		b, m := ghostFixture(t)
		b.locateErr = errors.New("docker daemon is not answering")
		if dropped := m.dropGhostReservations(context.Background()); dropped != 0 || m.Snapshot().Models[id(1)].ReservedMiB != 20 {
			t.Fatalf("an unanswered lookup released memory (dropped=%d)", dropped)
		}
	})
	t.Run("renamed but running", func(t *testing.T) {
		b, m := ghostFixture(t)
		b.presence = map[string]ContainerPresence{id(1): {Exists: true, Running: true, Name: "renamed"}}
		if dropped := m.dropGhostReservations(context.Background()); dropped != 0 {
			t.Fatalf("dropped=%d", dropped)
		}
		m.mu.Lock()
		name, reserved := m.models[id(1)].model.Name, m.models[id(1)].reserved
		m.mu.Unlock()
		if name != "renamed" || reserved != 20 {
			t.Fatalf("name=%q reserved=%d", name, reserved)
		}
	})
	t.Run("in use or in a deployment", func(t *testing.T) {
		for _, change := range []func(*Manager){
			func(m *Manager) { m.models[id(1)].active = true },
			func(m *Manager) { m.models[id(1)].blocked = true },
			func(m *Manager) { m.models[id(1)].preparing = true },
			func(m *Manager) { m.queue = []*waiter{{id: id(1)}} },
		} {
			b, m := ghostFixture(t)
			replaced(b, 1, 9, true)
			change(m)
			if dropped := m.dropGhostReservations(context.Background()); dropped != 0 || len(b.located) != 0 {
				t.Fatalf("a reservation owned by an operation was touched (dropped=%d located=%v)", dropped, b.located)
			}
		}
	})
}

func TestReplacementIsNotAdoptedWhenBusyOrAlreadyKnown(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		b, m := ghostFixture(t)
		replaced(b, 1, 9, true)
		b.inspect = func(_ context.Context, container string) (Model, api.ModelRuntime, error) {
			return b.models[container], api.ModelRuntime{State: api.RuntimeBusy, Loaded: true, ActiveRequests: 1}, nil
		}
		if dropped := m.dropGhostReservations(context.Background()); dropped != 1 {
			t.Fatalf("dropped=%d", dropped)
		}
		if _, known := m.Snapshot().Models[id(9)]; known {
			t.Fatal("a container serving a request was adopted")
		}
	})
	t.Run("already known", func(t *testing.T) {
		b, m := ghostFixture(t)
		replaced(b, 1, 9, true)
		m.models[id(9)] = &entry{model: b.models[id(9)], state: api.RuntimeReady, loaded: true, reserved: 33}
		if dropped := m.dropGhostReservations(context.Background()); dropped != 1 {
			t.Fatalf("dropped=%d", dropped)
		}
		if got := m.Snapshot().Models[id(9)]; got.ReservedMiB != 33 {
			t.Fatalf("an inventory entry was overwritten: %+v", got)
		}
	})
}

func TestRequestShortOfMemoryIsNotStalledByAGhostReservation(t *testing.T) {
	b, m := ghostFixture(t)
	m.models[id(1)].reserved = 80 // GPU A schedules 100 MiB
	b.add(2, 20, 50)
	assign(b, 2, gpuA)
	replaced(b, 1, 9, false)
	b.unload = func(_ context.Context, model Model) error {
		if model.ContainerID == id(1) {
			t.Error("the ghost was unloaded through its stale name instead of being dropped")
		}
		return nil
	}
	started := time.Now()
	if _, err := m.Predict(context.Background(), id(2), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	// Without the check the request waits for the ghost's idle timeout (1 s).
	if waited := time.Since(started); waited > 500*time.Millisecond {
		t.Fatalf("the request waited %s behind a reservation of a container that is gone", waited)
	}
	if snapshot := m.Snapshot(); snapshot.Models[id(1)].ReservedMiB != 0 || snapshot.GPUs[gpuA].ReservedMiB != 20 {
		t.Fatalf("unexpected ledger: %+v", snapshot.GPUs[gpuA])
	}
	if slices.Contains(b.unloads, id(1)) {
		t.Fatalf("unloads=%v", b.unloads)
	}
}

type locateDocker struct {
	Docker
	byRef map[string]docker.GetContainerInfoResult
	err   error
}

func (d *locateDocker) GetContainerInfo(_ context.Context, reference string) (docker.GetContainerInfoResult, error) {
	if d.err != nil {
		return docker.GetContainerInfoResult{}, d.err
	}
	if info, ok := d.byRef[reference]; ok {
		return info, nil
	}
	return docker.GetContainerInfoResult{}, fmt.Errorf("%w: %w", dockererrdefs.ErrNotFound, errors.New("no such container"))
}

func TestContainerBackendLocatesAReplacementOnlyByExactName(t *testing.T) {
	old := docker.GetContainerInfoResult{ID: id(1), Name: "/DeepRV-CLIP-pre-move", Lifecycle: docker.LifecycleStopped}
	replacement := docker.GetContainerInfoResult{ID: id(9), Name: "/DeepRV-CLIP", Lifecycle: docker.LifecycleRunning}
	d := &locateDocker{byRef: map[string]docker.GetContainerInfoResult{id(1): old, "DeepRV-CLIP": replacement}}
	backend := &ContainerBackend{Docker: d}

	presence, err := backend.Locate(context.Background(), id(1), "DeepRV-CLIP")
	if err != nil || !presence.Exists || !presence.Stopped || presence.Running || presence.ReplacementID != id(9) || !presence.gone() {
		t.Fatalf("stopped and renamed: %+v err=%v", presence, err)
	}

	delete(d.byRef, id(1)) // removed
	presence, err = backend.Locate(context.Background(), id(1), "DeepRV-CLIP")
	if err != nil || presence.Exists || presence.ReplacementID != id(9) || !presence.gone() {
		t.Fatalf("removed: %+v err=%v", presence, err)
	}

	// Docker's case-insensitive fallback must not pass for the replacement.
	d.byRef["DeepRV-CLIP"] = docker.GetContainerInfoResult{ID: id(9), Name: "/deeprv-clip", Lifecycle: docker.LifecycleRunning}
	if presence, _ = backend.Locate(context.Background(), id(1), "DeepRV-CLIP"); presence.ReplacementID != "" {
		t.Fatalf("a container with another name was taken for the replacement: %+v", presence)
	}

	// Still running under its own name: nothing else is looked up.
	d.byRef = map[string]docker.GetContainerInfoResult{id(1): {ID: id(1), Name: "/DeepRV-CLIP", Lifecycle: docker.LifecycleRunning}}
	presence, err = backend.Locate(context.Background(), id(1), "DeepRV-CLIP")
	if err != nil || !presence.Running || presence.gone() || presence.ReplacementID != "" {
		t.Fatalf("running: %+v err=%v", presence, err)
	}

	d.err = errors.New("docker daemon is not answering")
	if _, err = backend.Locate(context.Background(), id(1), "DeepRV-CLIP"); err == nil {
		t.Fatal("an unanswered lookup must not be read as a missing container")
	}
}
