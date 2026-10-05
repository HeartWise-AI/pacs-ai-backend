// Package modelmanager provides the single-host, single-GPU admission service.
// Callers must pass a trusted registered container ID, never a user-supplied URL.
package modelmanager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

var (
	ErrDisabled          = errors.New("model manager is disabled")
	ErrQueueTimeout      = errors.New("model admission queue timed out")
	ErrCapacity          = errors.New("model peak exceeds schedulable GPU budget")
	ErrUnmanagedResident = errors.New("unaccounted resident model requires startup reconciliation")
	ErrNotReconciled     = errors.New("model manager startup reconciliation is incomplete")
	ErrReconciling       = errors.New("model manager reconciliation is already running")
	containerIDPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// AdmissionTimeout is the retryable service result for Phase 4 HTTP adapters.
type AdmissionTimeout struct{}

func (*AdmissionTimeout) Error() string             { return ErrQueueTimeout.Error() }
func (*AdmissionTimeout) Unwrap() error             { return ErrQueueTimeout }
func (*AdmissionTimeout) HTTPStatus() int           { return http.StatusServiceUnavailable }
func (*AdmissionTimeout) RetryAfter() time.Duration { return time.Second }

type Model struct {
	ContainerID, Name string
	Resources         types.ModelResources
}

// Backend.Unload must confirm the old inference process has exited, not merely
// accept the Python endpoint's acknowledgement of a scheduled restart.
type Backend interface {
	Inspect(context.Context, string) (Model, types.ModelRuntime, error)
	Prepare(context.Context, string) (Model, error)
	Runtime(context.Context, Model) (types.ModelRuntime, error)
	Load(context.Context, Model) error
	Unload(context.Context, Model) error
	Predict(context.Context, Model, types.PredictRequest) (types.PredictResponse, error)
}

type entry struct {
	model    Model
	state    types.ModelRuntimeState
	reserved int
	active   bool // also protects preparation, load, cleanup and eviction
	leased   bool // includes CPU-only operations with a zero GPU reservation
	lastUsed time.Time
	loaded   bool
}
type waiter struct{ id string }

type Manager struct {
	mu          sync.Mutex
	config      Config
	backend     Backend
	models      map[string]*entry
	queue       []*waiter
	admitting   *waiter
	changed     chan struct{}
	ready       bool
	reconciling bool
	metrics     Metrics
}

func New(config Config, backend Backend) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("model lifecycle backend is required")
	}
	manager := &Manager{
		config: config, backend: backend, models: make(map[string]*entry),
		changed: make(chan struct{}), ready: !config.RequireReconciliation,
		metrics: defaultMetrics,
	}
	manager.metrics.SetSnapshot(manager.snapshotLocked())
	return manager, nil
}

func (m *Manager) wakeLocked() {
	m.metrics.SetSnapshot(m.snapshotLocked())
	close(m.changed)
	m.changed = make(chan struct{})
}
func (m *Manager) usedLocked() int {
	n := 0
	for _, e := range m.models {
		n += e.reserved
	}
	return n
}
func (m *Manager) capacity() int { return m.config.BudgetMiB - m.config.SafetyMarginMiB }

// Reconcile reconstructs the reservation ledger before the manager admits
// production traffic. Runtime failures with a valid resource contract are
// quarantined at peak; missing contracts fail the entire inventory closed.
// A definitively absent Docker container is an ERROR with no reservation: it
// cannot consume GPU memory and must not take unrelated registered models down.
func (m *Manager) Reconcile(ctx context.Context, containerIDs []string) (err error) {
	started := time.Now()
	outcome := "success"
	defer func() { m.metrics.ObserveReconciliation(outcome, time.Since(started)) }()

	m.mu.Lock()
	if m.reconciling {
		m.mu.Unlock()
		outcome = "rejected"
		return ErrReconciling
	}
	if len(m.queue) != 0 || m.admitting != nil {
		m.mu.Unlock()
		outcome = "rejected"
		return errors.New("cannot reconcile while admission is active")
	}
	for _, e := range m.models {
		if e.active {
			m.mu.Unlock()
			outcome = "rejected"
			return errors.New("cannot reconcile while a model operation is active")
		}
	}
	m.reconciling = true
	m.ready = false
	m.wakeLocked()
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.reconciling = false
		if err != nil {
			m.ready = false
		}
		m.wakeLocked()
		m.mu.Unlock()
	}()

	staged := make(map[string]*entry, len(containerIDs))
	seen := make(map[string]struct{}, len(containerIDs))
	reserved := 0
	quarantined := false
	for _, id := range containerIDs {
		if !containerIDPattern.MatchString(id) {
			outcome = "failed"
			return fmt.Errorf("reconcile container %q: full Docker container ID required", id)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		model, runtime, inspectErr := m.backend.Inspect(ctx, id)
		e := &entry{model: model, state: types.RuntimeUnloaded}
		if inspectErr != nil {
			if errors.Is(inspectErr, ErrContainerMissing) {
				e.state = types.RuntimeError
				quarantined = true
				m.metrics.ObserveEvent("reconciliation", "missing_container")
			} else if model.Resources.Validate() != nil {
				outcome = "failed"
				return fmt.Errorf("reconcile %s without a trustworthy resource contract: %w", id, inspectErr)
			} else {
				e.state = types.RuntimeError
				e.reserved = model.Resources.PeakMemoryMiB
				e.loaded = true
				quarantined = true
			}
		} else if runtime.State == types.RuntimeUnloaded {
			if err = runtime.Validate(); err != nil {
				outcome = "failed"
				return fmt.Errorf("reconcile %s: %w", id, err)
			}
		} else {
			if err = model.Resources.Validate(); err != nil {
				outcome = "failed"
				return fmt.Errorf("reconcile %s resources: %w", id, err)
			}
			if err = runtime.Validate(); err != nil {
				outcome = "failed"
				return fmt.Errorf("reconcile %s runtime: %w", id, err)
			}
			e.loaded = runtime.Loaded
			if runtime.State == types.RuntimeReady && runtime.Loaded && runtime.ActiveRequests == 0 {
				e.state = types.RuntimeReady
				e.reserved = model.Resources.ResidentMemoryMiB
				if runtime.LastUsedAt != nil {
					e.lastUsed = *runtime.LastUsedAt
				} else {
					e.lastUsed = time.Now()
				}
			} else {
				e.state = types.RuntimeError
				e.reserved = model.Resources.PeakMemoryMiB
				quarantined = true
			}
		}
		reserved += e.reserved
		if reserved > m.capacity() {
			outcome = "failed"
			return errors.New("reconciled reservations exceed schedulable GPU capacity")
		}
		staged[id] = e
	}

	m.mu.Lock()
	m.models = staged
	m.ready = true
	m.wakeLocked()
	m.mu.Unlock()
	if quarantined {
		outcome = "quarantined"
	}
	return nil
}

// Predict owns the lease through confirmed completion or cleanup. A canceled
// HTTP request may leave Python computing; cleanup uses an independent bounded
// context. Unconfirmed cleanup keeps an explicit ERROR reservation fail-closed.
func (m *Manager) Predict(ctx context.Context, id string, request types.PredictRequest) (response types.PredictResponse, err error) {
	if !m.config.Enabled {
		return response, ErrDisabled
	}
	if !containerIDPattern.MatchString(id) {
		return response, errors.New("a full registered Docker container ID is required")
	}
	queueCtx, cancelQueue := context.WithTimeout(ctx, m.config.QueueTimeout)
	defer cancelQueue()
	queueStarted := time.Now()
	w, e, err := m.admit(queueCtx, id)
	if err != nil {
		outcome := metricOutcome(queueError(ctx, queueCtx, err))
		m.metrics.ObserveQueueWait(outcome, time.Since(queueStarted))
		m.metrics.ObserveEvent("admission_failure", outcome)
		return response, queueError(ctx, queueCtx, err)
	}
	m.metrics.ObserveQueueWait("admitted", time.Since(queueStarted))
	executionStarted := time.Now()
	defer func() { m.metrics.ObserveOperation("execution", metricOutcome(err), time.Since(executionStarted)) }()
	success := false
	defer func() {
		p := recover()
		cleanupErr := m.finish(ctx, w, e, success && p == nil)
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
		if p != nil {
			panic(p)
		}
	}()
	// Recover a prior uncertain operation before attempting a new reservation.
	m.mu.Lock()
	quarantined := e.state == types.RuntimeError && (e.reserved > 0 || e.leased)
	oldModel := e.model
	m.mu.Unlock()
	if quarantined {
		if err = m.backend.Unload(queueCtx, oldModel); err != nil {
			m.metrics.ObserveEvent("recovery", "failed")
			return response, queueError(ctx, queueCtx, err)
		}
		m.mu.Lock()
		e.reserved = 0
		e.leased = false
		e.state = types.RuntimeUnloaded
		e.loaded = false
		m.wakeLocked()
		m.mu.Unlock()
		m.metrics.ObserveEvent("recovery", "success")
	}
	model, prepareErr := m.backend.Prepare(queueCtx, id)
	if prepareErr != nil {
		return response, queueError(ctx, queueCtx, prepareErr)
	}
	if model.ContainerID != id {
		return response, errors.New("lifecycle backend returned a different container")
	}
	if err = model.Resources.Validate(); err != nil {
		return response, err
	}
	m.mu.Lock()
	if e.reserved > 0 && e.model.Resources != model.Resources {
		m.mu.Unlock()
		return response, errors.New("resident model resource contract changed")
	}
	unaccounted := e.reserved == 0 && !e.leased
	e.model = model
	// Discovery is serialized with admission. Until runtime is known, retain a
	// conservative peak reservation; a failed runtime probe must not free it.
	if unaccounted {
		e.reserved = model.Resources.PeakMemoryMiB
		e.leased = true
	}
	m.mu.Unlock()
	runtime, runtimeErr := m.backend.Runtime(queueCtx, model)
	if runtimeErr != nil {
		return response, queueError(ctx, queueCtx, runtimeErr)
	}
	if err = runtime.Validate(); err != nil {
		return response, err
	}
	if runtime.ActiveRequests != 0 || (runtime.State != types.RuntimeReady && runtime.State != types.RuntimeUnloaded) {
		return response, errors.New("model runtime is not idle")
	}
	m.mu.Lock()
	e.loaded = runtime.Loaded
	if runtime.Loaded && unaccounted && model.Resources.GPURequired {
		// First-touch discovery cannot authorize more GPU work. Account for this
		// resident immediately and clean it up before permitting a retry.
		e.reserved = model.Resources.PeakMemoryMiB
		e.leased = true
		m.mu.Unlock()
		return response, ErrUnmanagedResident
	}
	if !runtime.Loaded {
		e.reserved = 0
		e.leased = false
		e.state = types.RuntimeUnloaded
		e.loaded = false
		m.wakeLocked()
	}
	m.mu.Unlock()
	if err = m.reserve(queueCtx, e); err != nil {
		m.metrics.ObserveEvent("admission_failure", metricOutcome(err))
		return response, queueError(ctx, queueCtx, err)
	}
	// Admission is finished; other models may now load concurrently within budget.
	m.mu.Lock()
	m.admitting = nil
	m.wakeLocked()
	m.mu.Unlock()
	cancelQueue()
	opCtx, cancelOperation := context.WithTimeout(ctx, m.config.OperationTimeout)
	defer cancelOperation()
	if !runtime.Loaded {
		loadStarted := time.Now()
		if err = m.backend.Load(opCtx, model); err != nil {
			m.metrics.ObserveOperation("cold_start", "failed", time.Since(loadStarted))
			return response, errors.Join(opCtx.Err(), err)
		}
		m.metrics.ObserveOperation("cold_start", "success", time.Since(loadStarted))
		m.mu.Lock()
		e.loaded = true
		m.wakeLocked()
		m.mu.Unlock()
	}
	if err = opCtx.Err(); err != nil {
		return response, err
	}
	m.mu.Lock()
	e.state = types.RuntimeBusy
	m.wakeLocked()
	m.mu.Unlock()
	inferenceStarted := time.Now()
	response, err = m.backend.Predict(opCtx, model, request)
	if err == nil && !response.Success {
		err = errors.New("model prediction reported failure")
	}
	// The legacy prediction client returns an opaque provider error on transport
	// cancellation. Preserve the operation context's cause for callers as well.
	err = errors.Join(opCtx.Err(), err)
	temperature := "cold_inference"
	if runtime.Loaded {
		temperature = "warm_inference"
	}
	m.metrics.ObserveOperation(temperature, metricOutcome(err), time.Since(inferenceStarted))
	success = err == nil
	return response, err
}

func queueError(parent, queue context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if errors.Is(queue.Err(), context.DeadlineExceeded) {
		return &AdmissionTimeout{}
	}
	return err
}

// Select the oldest eligible request. A busy model never blocks a different
// model's queue, while requests targeting the same model retain FIFO order.
func (m *Manager) admit(ctx context.Context, id string) (*waiter, *entry, error) {
	m.mu.Lock()
	// Readiness and queue insertion must be one atomic decision. Otherwise a
	// reconciliation could start after Predict's readiness check but before this
	// request becomes visible to Reconcile.
	if !m.ready || m.reconciling {
		m.mu.Unlock()
		return nil, nil, ErrNotReconciled
	}
	if _, ok := m.models[id]; !ok {
		m.models[id] = &entry{state: types.RuntimeUnloaded}
	}
	w := &waiter{id: id}
	m.queue = append(m.queue, w)
	m.metrics.SetSnapshot(m.snapshotLocked())
	for {
		if err := ctx.Err(); err != nil {
			m.removeLocked(w)
			m.wakeLocked()
			m.mu.Unlock()
			return nil, nil, err
		}
		var first *waiter
		for _, candidate := range m.queue {
			if !m.models[candidate.id].active {
				first = candidate
				break
			}
		}
		if m.admitting == nil && first == w {
			m.removeLocked(w)
			e := m.models[id]
			e.active = true
			m.admitting = w
			m.metrics.SetSnapshot(m.snapshotLocked())
			m.mu.Unlock()
			return w, e, nil
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		m.mu.Lock()
	}
}
func (m *Manager) removeLocked(w *waiter) {
	for i, v := range m.queue {
		if v == w {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

func (m *Manager) reserve(ctx context.Context, target *entry) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		peak := target.model.Resources.PeakMemoryMiB
		if peak > m.capacity() {
			m.mu.Unlock()
			return ErrCapacity
		}
		if peak-target.reserved <= m.capacity()-m.usedLocked() {
			target.reserved = peak
			target.leased = true
			target.state = types.RuntimeLoading
			m.wakeLocked()
			m.mu.Unlock()
			return nil
		}
		var victim *entry
		wait := m.config.QueueTimeout
		now := time.Now()
		for _, e := range m.models {
			if e == target || e.active || e.state != types.RuntimeReady || e.reserved == 0 {
				continue
			}
			remaining := time.Duration(e.model.Resources.IdleTimeoutSeconds)*time.Second - now.Sub(e.lastUsed)
			if remaining > 0 {
				if remaining < wait {
					wait = remaining
				}
				continue
			}
			if victim == nil || e.lastUsed.Before(victim.lastUsed) || (e.lastUsed.Equal(victim.lastUsed) && e.model.ContainerID < victim.model.ContainerID) {
				victim = e
			}
		}
		if victim != nil {
			victim.active = true
			victim.state = types.RuntimeEvicting
			model := victim.model
			m.wakeLocked()
			m.mu.Unlock()
			err := m.evict(ctx, victim, model)
			if err != nil {
				return fmt.Errorf("idle model eviction failed: %w", err)
			}
			continue
		}
		changed := m.changed
		m.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (m *Manager) evict(ctx context.Context, e *entry, model Model) (err error) {
	started := time.Now()
	defer func() {
		p := recover()
		m.mu.Lock()
		e.active = false
		if err == nil && p == nil {
			e.reserved = 0
			e.state = types.RuntimeUnloaded
			e.loaded = false
		} else {
			e.state = types.RuntimeError
		}
		m.wakeLocked()
		m.mu.Unlock()
		outcome := metricOutcome(err)
		if p != nil {
			outcome = "panic"
		}
		m.metrics.ObserveOperation("eviction", outcome, time.Since(started))
		m.metrics.ObserveEvent("eviction", outcome)
		if p != nil {
			panic(p)
		}
	}()
	return m.backend.Unload(ctx, model)
}

func (m *Manager) finish(ctx context.Context, w *waiter, e *entry, success bool) (err error) {
	m.mu.Lock()
	model := e.model
	needsCleanup := e.reserved > 0 || e.leased
	if !success && needsCleanup {
		e.state = types.RuntimeEvicting
		m.wakeLocked()
	}
	m.mu.Unlock()
	// Always release queue ownership, including if a backend implementation panics.
	defer func() {
		p := recover()
		m.mu.Lock()
		if success && p == nil {
			e.reserved = e.model.Resources.ResidentMemoryMiB
			e.leased = false
			e.state = types.RuntimeReady
			e.lastUsed = time.Now()
			e.loaded = true
		} else if err == nil && p == nil {
			e.reserved = 0
			e.leased = false
			e.state = types.RuntimeUnloaded
			e.loaded = false
		} else {
			e.state = types.RuntimeError
		}
		e.active = false
		if m.admitting == w {
			m.admitting = nil
		}
		m.wakeLocked()
		m.mu.Unlock()
		if p != nil {
			panic(p)
		}
	}()
	if !success && needsCleanup {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.config.OperationTimeout)
		defer cancel()
		if err = m.backend.Unload(cleanupCtx, model); err != nil {
			m.metrics.ObserveEvent("cleanup", "failed")
			return fmt.Errorf("GPU reservation retained until cleanup is confirmed: %w", err)
		}
		m.metrics.ObserveEvent("cleanup", "success")
	}
	return nil
}

type ModelSnapshot struct {
	State       types.ModelRuntimeState
	ReservedMiB int
	Active      bool
	LastUsedAt  time.Time
	QueueDepth  int
	Loaded      bool
}
type Snapshot struct {
	Ready                                 bool
	CapacityMiB, ResidentMiB, ActiveMiB   int
	ReservedMiB, AvailableMiB, QueueDepth int
	Models                                map[string]ModelSnapshot
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() Snapshot {
	s := Snapshot{Ready: m.ready && !m.reconciling, CapacityMiB: m.capacity(), ReservedMiB: m.usedLocked(), Models: make(map[string]ModelSnapshot)}
	for id, e := range m.models {
		s.Models[id] = ModelSnapshot{State: e.state, ReservedMiB: e.reserved, Active: e.active, LastUsedAt: e.lastUsed, Loaded: e.loaded}
		resident := 0
		if e.loaded && e.model.Resources.GPURequired {
			resident = min(e.reserved, e.model.Resources.ResidentMemoryMiB)
		}
		s.ResidentMiB += resident
		s.ActiveMiB += e.reserved - resident
	}
	for _, w := range m.queue {
		v := s.Models[w.id]
		v.QueueDepth++
		s.Models[w.id] = v
		s.QueueDepth++
	}
	s.AvailableMiB = max(0, s.CapacityMiB-s.ReservedMiB)
	return s
}
