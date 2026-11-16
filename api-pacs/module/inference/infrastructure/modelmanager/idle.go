package modelmanager

import (
	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	"context"
	"time"
)

// RunIdleEviction releases models after their declared idle timeout even when
// no request is waiting for memory. Automated models use the same idle policy:
// ingestion runs reload them through the managed gateway on demand.
func (m *Manager) RunIdleEviction(ctx context.Context) {
	if !m.config.Enabled || m.config.IdleSweepInterval <= 0 {
		return
	}
	ticker := time.NewTicker(m.config.IdleSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.evictIdle(ctx)
		}
	}
}

// recoverQuarantined retries the unload of models whose earlier unload or
// inference left them in ERROR with a reservation. Without it such a
// reservation is only released when that same model is requested again, so a
// few failures can leave a GPU fully reserved with nothing loaded.
func (m *Manager) recoverQuarantined(ctx context.Context) {
	for ctx.Err() == nil {
		m.mu.Lock()
		if !m.ready || m.reconciling {
			m.mu.Unlock()
			return
		}
		now := time.Now()
		var victim *entry
		for _, e := range m.models {
			if e.active || e.blocked || e.draining || e.preparing || e.state != api.RuntimeError || (e.reserved == 0 && !e.leased) || now.Before(e.recoverAfter) {
				continue
			}
			victim = e
			break
		}
		if victim == nil {
			m.mu.Unlock()
			return
		}
		victim.active = true
		model := victim.model
		m.mu.Unlock()
		unloadCtx, cancel := context.WithTimeout(ctx, m.config.OperationTimeout)
		err := m.backend.Unload(unloadCtx, model)
		cancel()
		m.mu.Lock()
		victim.active = false
		if err == nil {
			victim.reserved = 0
			victim.leased = false
			victim.loaded = false
			victim.state = api.RuntimeUnloaded
			victim.recoverFailed = 0
			victim.recoverAfter = time.Time{}
		} else {
			victim.recoverFailed++
			backoff := time.Duration(victim.recoverFailed) * time.Minute
			if backoff > 10*time.Minute {
				backoff = 10 * time.Minute
			}
			victim.recoverAfter = time.Now().Add(backoff)
		}
		m.wakeLocked()
		m.mu.Unlock()
		m.metrics.ObserveEvent("recovery", metricOutcome(err))
	}
}

func (m *Manager) evictIdle(ctx context.Context) {
	// First forget reservations of containers that are gone: recovering or
	// evicting one would address whatever container now holds its name.
	m.dropGhostReservations(ctx)
	m.recoverQuarantined(ctx)
	// Each victim is claimed under the same lock as inference/deployment. An
	// unload failure quarantines its reservation rather than freeing capacity.
	for ctx.Err() == nil {
		m.mu.Lock()
		if !m.ready || m.reconciling {
			m.mu.Unlock()
			return
		}
		var victim *entry
		for id, e := range m.models {
			if e.active || e.blocked || e.leased || e.state != api.RuntimeReady || !e.loaded || m.hasQueuedLocked(id) {
				continue
			}
			if time.Since(e.lastUsed) < time.Duration(e.model.Resources.IdleTimeoutSeconds)*time.Second {
				continue
			}
			if victim == nil || e.lastUsed.Before(victim.lastUsed) {
				victim = e
			}
		}
		if victim == nil {
			m.mu.Unlock()
			return
		}
		victim.active = true
		victim.state = api.RuntimeEvicting
		model := victim.model
		m.wakeLocked()
		m.mu.Unlock()
		unloadCtx, cancel := context.WithTimeout(ctx, m.config.OperationTimeout)
		_ = m.evict(unloadCtx, victim, model)
		cancel()
	}
}
