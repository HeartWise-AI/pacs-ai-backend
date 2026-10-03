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
}
type waiter struct{ id string }

type Manager struct {
	mu        sync.Mutex
	config    Config
	backend   Backend
	models    map[string]*entry
	queue     []*waiter
	admitting *waiter
	changed   chan struct{}
}

func New(config Config, backend Backend) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("model lifecycle backend is required")
	}
	return &Manager{config: config, backend: backend, models: make(map[string]*entry), changed: make(chan struct{})}, nil
}

func (m *Manager) wakeLocked() { close(m.changed); m.changed = make(chan struct{}) }
func (m *Manager) usedLocked() int {
	n := 0
	for _, e := range m.models {
		n += e.reserved
	}
	return n
}
func (m *Manager) capacity() int { return m.config.BudgetMiB - m.config.SafetyMarginMiB }

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
	w, e, err := m.admit(queueCtx, id)
	if err != nil {
		return response, queueError(ctx, queueCtx, err)
	}
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
			return response, queueError(ctx, queueCtx, err)
		}
		m.mu.Lock()
		e.reserved = 0
		e.leased = false
		e.state = types.RuntimeUnloaded
		m.wakeLocked()
		m.mu.Unlock()
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
		m.wakeLocked()
	}
	m.mu.Unlock()
	if err = m.reserve(queueCtx, e); err != nil {
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
		if err = m.backend.Load(opCtx, model); err != nil {
			return response, err
		}
	}
	if err = opCtx.Err(); err != nil {
		return response, err
	}
	m.mu.Lock()
	e.state = types.RuntimeBusy
	m.mu.Unlock()
	response, err = m.backend.Predict(opCtx, model, request)
	if err == nil && !response.Success {
		err = errors.New("model prediction reported failure")
	}
	if err == nil {
		err = opCtx.Err()
	}
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
	if _, ok := m.models[id]; !ok {
		m.models[id] = &entry{state: types.RuntimeUnloaded}
	}
	w := &waiter{id: id}
	m.queue = append(m.queue, w)
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
	defer func() {
		p := recover()
		m.mu.Lock()
		e.active = false
		if err == nil && p == nil {
			e.reserved = 0
			e.state = types.RuntimeUnloaded
		} else {
			e.state = types.RuntimeError
		}
		m.wakeLocked()
		m.mu.Unlock()
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
		} else if err == nil && p == nil {
			e.reserved = 0
			e.leased = false
			e.state = types.RuntimeUnloaded
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
			return fmt.Errorf("GPU reservation retained until cleanup is confirmed: %w", err)
		}
	}
	return nil
}

type ModelSnapshot struct {
	State       types.ModelRuntimeState
	ReservedMiB int
	Active      bool
	LastUsedAt  time.Time
	QueueDepth  int
}
type Snapshot struct {
	CapacityMiB, ReservedMiB int
	Models                   map[string]ModelSnapshot
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{CapacityMiB: m.capacity(), ReservedMiB: m.usedLocked(), Models: make(map[string]ModelSnapshot)}
	for id, e := range m.models {
		s.Models[id] = ModelSnapshot{State: e.state, ReservedMiB: e.reserved, Active: e.active, LastUsedAt: e.lastUsed}
	}
	for _, w := range m.queue {
		v := s.Models[w.id]
		v.QueueDepth++
		s.Models[w.id] = v
	}
	return s
}
