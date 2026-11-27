package modelmanager

import (
	"context"
	"log"
	"strings"
	"time"

	dockererrdefs "github.com/containerd/errdefs"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
)

const (
	// ghostLookupTimeout bounds the Docker lookups made for one reservation.
	ghostLookupTimeout = 5 * time.Second
	// ghostSweepGap limits how often a request that is short of memory may
	// trigger a check of the reservations against Docker.
	ghostSweepGap = 5 * time.Second
	// ghostSweepInterval is the pace of the periodic check. It costs one
	// Docker inspection per model that holds a reservation.
	ghostSweepInterval = 30 * time.Second
)

// ContainerPresence is what Docker says about a container the manager holds a
// reservation for. A container that no longer exists, or exists but is stopped,
// has no process and therefore no GPU memory.
type ContainerPresence struct {
	Exists  bool
	Stopped bool
	Running bool
	// Name is the container's current name; it changes when the container is
	// renamed, for example when it is kept as a rollback copy.
	Name string
	// ReplacementID is the full ID of another container that now carries the
	// name the manager knew this container by; empty when there is none.
	ReplacementID string
}

func (presence ContainerPresence) gone() bool { return !presence.Exists || presence.Stopped }

// ContainerLocator is implemented by lifecycle backends that can tell whether a
// container still exists. Without it reservations are never reconciled.
type ContainerLocator interface {
	Locate(ctx context.Context, containerID, knownName string) (ContainerPresence, error)
}

// Locate inspects a container by ID and, when it is gone or was renamed, looks
// for the container that now holds its former name. Docker names are unique, so
// such a container is the unambiguous replacement.
func (b *ContainerBackend) Locate(ctx context.Context, id, knownName string) (ContainerPresence, error) {
	presence := ContainerPresence{}
	info, err := b.Docker.GetContainerInfo(ctx, id)
	switch {
	case err == nil && info.ID != "" && info.ID != id:
		// A name or prefix matched something else: the container itself is gone.
	case err == nil:
		presence.Exists = true
		presence.Name = strings.TrimPrefix(info.Name, "/")
		presence.Running = info.Lifecycle == docker.LifecycleRunning
		presence.Stopped = info.Lifecycle == docker.LifecycleStopped
	case !dockererrdefs.IsNotFound(err):
		return presence, err // unknown: keep the reservation
	}
	knownName = strings.TrimPrefix(strings.TrimSpace(knownName), "/")
	if knownName == "" || (presence.Exists && presence.Name == knownName) {
		return presence, nil
	}
	holder, err := b.Docker.GetContainerInfo(ctx, knownName)
	if err == nil && holder.ID != id && containerIDPattern.MatchString(holder.ID) && strings.TrimPrefix(holder.Name, "/") == knownName {
		presence.ReplacementID = holder.ID
	}
	return presence, nil
}

// RunReservationReconciliation periodically drops reservations of containers
// that no longer exist (see dropGhostReservations). It does not depend on the
// idle sweep, which is disabled by default.
func (m *Manager) RunReservationReconciliation(ctx context.Context) {
	if !m.config.Enabled {
		return
	}
	if _, ok := m.backend.(ContainerLocator); !ok {
		return
	}
	ticker := time.NewTicker(ghostSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.dropGhostReservations(ctx)
		}
	}
}

// ghostSweepDueLocked reports whether a request short of memory should check
// the reservations against Docker now, and records that it does.
func (m *Manager) ghostSweepDueLocked(now time.Time) bool {
	if _, ok := m.backend.(ContainerLocator); !ok || now.Sub(m.lastGhostSweep) < ghostSweepGap {
		return false
	}
	m.lastGhostSweep = now
	return true
}

// dropGhostReservations reconciles the reservation ledger with the containers
// that actually exist. A model container can be replaced outside the gateway:
// the old one is stopped or removed and another container takes its name.
// The manager would otherwise keep the old container's reservation, and every
// attempt to release it would address the new container by the old name.
//
// A reservation whose container is gone is dropped. If another container now
// holds that name, is not yet in the inventory and is idle, it is adopted with
// the reservation its real state calls for. Each reservation is claimed like an
// eviction while it is checked, so it cannot be admitted meanwhile. It returns
// how many reservations were dropped.
func (m *Manager) dropGhostReservations(ctx context.Context) int {
	locator, ok := m.backend.(ContainerLocator)
	if !ok {
		return 0
	}
	dropped := 0
	checked := map[*entry]struct{}{}
	for ctx.Err() == nil {
		m.mu.Lock()
		if !m.ready || m.reconciling {
			m.mu.Unlock()
			return dropped
		}
		m.lastGhostSweep = time.Now()
		var target *entry
		targetID := ""
		for id, e := range m.models {
			if _, done := checked[e]; done || e.active || e.blocked || e.draining || e.preparing ||
				(e.reserved == 0 && !e.leased) || e.model.ContainerID != id || m.hasQueuedLocked(id) {
				continue
			}
			if target == nil || id < targetID {
				target, targetID = e, id
			}
		}
		if target == nil {
			m.mu.Unlock()
			return dropped
		}
		checked[target] = struct{}{}
		target.active = true
		model := target.model
		m.mu.Unlock()

		lookupCtx, cancel := context.WithTimeout(ctx, ghostLookupTimeout)
		presence, err := locator.Locate(lookupCtx, targetID, model.Name)
		var adopted *entry
		adoptNote := ""
		if err == nil && presence.gone() && presence.ReplacementID != "" {
			adopted, adoptNote = m.inspectReplacement(lookupCtx, presence.ReplacementID)
		}
		cancel()

		m.mu.Lock()
		target.active = false
		switch {
		case err != nil:
			// Docker did not answer: the reservation stays.
		case presence.gone():
			released := target.reserved
			reason := "container_stopped"
			target.reserved, target.leased, target.loaded = 0, false, false
			target.state = types.RuntimeUnloaded
			target.recoverAfter, target.recoverFailed = time.Time{}, 0
			if !presence.Exists {
				// Same as an absent container at startup: no reservation.
				reason, target.state = "container_missing", types.RuntimeError
			}
			dropped++
			m.metrics.ObserveEvent("ghost_reservation", "dropped")
			log.Printf("[model-manager] ghost reservation dropped container=%.12s name=%q gpu=%s released_mib=%d reason=%s replacement=%.12s",
				targetID, model.Name, model.GPUUUID, released, reason, presence.ReplacementID)
			if presence.ReplacementID == "" {
				break
			}
			if _, known := m.models[presence.ReplacementID]; known {
				log.Printf("[model-manager] replacement container=%.12s of name=%q is already in the inventory", presence.ReplacementID, model.Name)
				break
			}
			if adopted == nil {
				log.Printf("[model-manager] replacement container=%.12s of name=%q not adopted: %s; it is accounted for at its first request", presence.ReplacementID, model.Name, adoptNote)
				break
			}
			m.models[presence.ReplacementID] = adopted
			m.metrics.ObserveEvent("ghost_reservation", "adopted")
			log.Printf("[model-manager] replacement adopted name=%q old_container=%.12s new_container=%.12s gpu=%s state=%s reserved_mib=%d",
				model.Name, targetID, presence.ReplacementID, adopted.model.GPUUUID, adopted.state, adopted.reserved)
		case presence.Running && presence.Name != "" && presence.Name != model.Name:
			// Renamed but alive: keep the reservation and address the
			// container by its current name from now on.
			log.Printf("[model-manager] container=%.12s was renamed from %q to %q; reservation kept", targetID, model.Name, presence.Name)
			target.model.Name = presence.Name
		}
		m.wakeLocked()
		m.mu.Unlock()
	}
	return dropped
}

// inspectReplacement builds the ledger entry of a container that replaced
// another one outside the gateway. Only an idle container is adopted: one that
// is unloaded holds nothing, one that is loaded and idle holds its resident
// reservation. Anything else is left to the first request for it.
func (m *Manager) inspectReplacement(ctx context.Context, id string) (*entry, string) {
	model, runtime, err := m.backend.Inspect(ctx, id)
	if err != nil {
		return nil, "it could not be inspected (" + err.Error() + ")"
	}
	if model.ContainerID != id {
		return nil, "the backend described another container"
	}
	if runtime.State == types.RuntimeUnloaded && !runtime.Loaded && runtime.ActiveRequests == 0 {
		return &entry{model: model, state: types.RuntimeUnloaded}, ""
	}
	if runtime.Validate() != nil || model.Resources.Validate() != nil {
		return nil, "its runtime or resource contract is invalid"
	}
	if runtime.State != types.RuntimeReady || !runtime.Loaded || runtime.ActiveRequests != 0 {
		return nil, "it is not idle (state " + string(runtime.State) + ")"
	}
	if err = m.validatePlacement(model); err != nil {
		return nil, "it has no managed GPU placement"
	}
	adopted := &entry{model: model, state: types.RuntimeReady, loaded: true, lastUsed: time.Now()}
	if runtime.LastUsedAt != nil {
		adopted.lastUsed = *runtime.LastUsedAt
	}
	m.mu.Lock()
	adopted.reserved = m.residentReservation(model)
	m.mu.Unlock()
	return adopted, ""
}

var _ ContainerLocator = (*ContainerBackend)(nil)
