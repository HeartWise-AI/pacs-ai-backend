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

func (m *Manager) evictIdle(ctx context.Context) {
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
