// Package modelmanager provides the single-host, per-GPU admission service.
// Callers must pass a trusted registered container ID, never a user-supplied URL.
package modelmanager

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
)

var (
	ErrDisabled          = errors.New("model manager is disabled")
	ErrQueueTimeout      = errors.New("model admission queue timed out")
	ErrCapacity          = errors.New("model peak exceeds schedulable GPU budget")
	ErrUnmanagedResident = errors.New("unaccounted resident model requires startup reconciliation")
	ErrNotReconciled     = errors.New("model manager startup reconciliation is incomplete")
	ErrReconciling       = errors.New("model manager reconciliation is already running")
	ErrAdmissionBlocked  = errors.New("model admission is blocked for a deployment transition")
	// ErrModelBusy refuses an operation that must not disturb a model with an
	// admitted or waiting request.
	ErrModelBusy       = errors.New("model is serving or has waiting requests")
	containerIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// AdmissionTimeout is the retryable service result for Phase 4 HTTP adapters.
type AdmissionTimeout struct{}

func (*AdmissionTimeout) Error() string             { return ErrQueueTimeout.Error() }
func (*AdmissionTimeout) Unwrap() error             { return ErrQueueTimeout }
func (*AdmissionTimeout) HTTPStatus() int           { return http.StatusServiceUnavailable }
func (*AdmissionTimeout) RetryAfter() time.Duration { return time.Second }

// AdmissionBlocked tells callers to retry once a deployment transition ends.
type AdmissionBlocked struct{}

func (*AdmissionBlocked) Error() string             { return ErrAdmissionBlocked.Error() }
func (*AdmissionBlocked) Unwrap() error             { return ErrAdmissionBlocked }
func (*AdmissionBlocked) HTTPStatus() int           { return http.StatusServiceUnavailable }
func (*AdmissionBlocked) RetryAfter() time.Duration { return time.Second }
func (*AdmissionBlocked) ErrorCode() string         { return apiError.InferenceAdmissionBlocked }
func (*AdmissionBlocked) PublicMessage() string {
	return "The model is temporarily unavailable during a deployment transition."
}

type Model struct {
	ContainerID, Name string
	GPUUUID           string
	// ImageID is the local image the container was created from. Measurements
	// follow a model to a replacement container only when it is unchanged.
	ImageID   string
	Resources types.ModelResources
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
	model     Model
	state     types.ModelRuntimeState
	reserved  int
	active    bool // also protects preparation, load, cleanup and eviction
	leased    bool // includes CPU-only operations with a zero GPU reservation
	lastUsed  time.Time
	loaded    bool
	blocked   bool // rejects new and queued admission during deployment changes
	draining  bool // grants one drain operation exclusive ownership of the gate
	preparing bool // prevents activation before candidate preparation completes
	// recoverAfter delays the next background attempt to release a quarantined reservation.
	recoverAfter  time.Time
	recoverFailed int
	// sampledActive records that the GPU sampler saw this model's process
	// during the prediction in progress, so its completion counts as observed.
	sampledActive bool
	// busySince and busySamples describe the prediction in progress.
	busySince   time.Time
	busySamples int
}
type waiter struct{ id string }

type Manager struct {
	mu                sync.Mutex
	config            Config
	backend           Backend
	models            map[string]*entry
	queue             []*waiter
	admitting         *waiter
	residentAdmission map[string]*waiter // serializes resident-to-peak promotion per GPU
	changed           chan struct{}
	ready             bool
	reconciling       bool
	metrics           Metrics
	// measMu guards the measured-memory state below. It is a leaf lock: it may
	// be taken while holding mu, never the reverse, and never across I/O.
	// Every writer also holds mu, so code holding mu reads a stable view.
	measMu          sync.Mutex
	measurements    map[string]*measurement
	removedMeasures []string
	replaces        map[string]string // candidate container ID -> container it replaces
	gpuUsage        map[string]GPUUsage
	kicked          map[string]struct{} // GPUs with a sample requested by a prediction
	measureKick     chan struct{}
	// lastGhostSweep is when reservations were last checked against the
	// containers that exist; guarded by mu.
	lastGhostSweep time.Time
}

func New(config Config, backend Backend) (*Manager, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("model lifecycle backend is required")
	}
	if config.ReservationSource == "" {
		config.ReservationSource = ReservationDeclared
	}
	if config.MeasuredMinSamples == 0 {
		config.MeasuredMinSamples = defaultMeasuredMinSamples
	}
	if config.MeasuredSafetyPercent == 0 {
		config.MeasuredSafetyPercent = defaultMeasuredSafetyPercent
	}
	if config.MeasuredMinBusy == 0 {
		config.MeasuredMinBusy = defaultMeasuredMinBusy
	}
	if config.PlacementMinIdle == 0 {
		config.PlacementMinIdle = defaultPlacementMinIdle
	}
	if config.PlacementMode == "" {
		config.PlacementMode = PlacementPinned
	}
	if config.PlacementActor == "" {
		config.PlacementActor = defaultPlacementActor
	}
	manager := &Manager{
		config: config, backend: backend, models: make(map[string]*entry),
		changed: make(chan struct{}), ready: !config.RequireReconciliation,
		residentAdmission: make(map[string]*waiter),
		metrics:           defaultMetrics,
		measurements:      make(map[string]*measurement),
		replaces:          make(map[string]string),
		gpuUsage:          make(map[string]GPUUsage),
		kicked:            make(map[string]struct{}),
		measureKick:       make(chan struct{}, 1),
	}
	manager.metrics.SetSnapshot(manager.snapshotLocked())
	return manager, nil
}

func (m *Manager) wakeLocked() {
	m.metrics.SetSnapshot(m.snapshotLocked())
	close(m.changed)
	m.changed = make(chan struct{})
}

// A READY response confirms completion, not that CUDA allocator caches were
// released. Keep the peak until confirmed process exit unless the operator has
// explicitly validated lower steady-state residency for every admitted model.
func (m *Manager) residentReservation(model Model) int {
	if m.config.RetainPeakWhileLoaded && model.Resources.GPURequired {
		return m.peakReservation(model)
	}
	resident := model.Resources.ResidentMemoryMiB
	if measured, ok := m.measuredResident(model); ok {
		resident = min(measured, resident)
	}
	if !model.Resources.GPURequired {
		return resident
	}
	// What a loaded model holds never exceeds what it was admitted with: a
	// measured peak reservation may be below the declared resident value.
	return min(resident, m.peakReservation(model))
}

// Model declarations are immutable image metadata, not instantaneous memory
// measurements. Optional conservative scaling compensates for underestimated
// validated peaks without modifying the model's identity or resource contract.
func (m *Manager) peakReservation(model Model) int {
	declared := m.declaredReservation(model)
	if measured, ok := m.measuredPeak(model); ok {
		return min(measured, declared)
	}
	return declared
}

// declaredReservation is the reservation derived from image metadata alone. It
// is the ceiling of every measured reservation.
func (m *Manager) declaredReservation(model Model) int {
	peak := model.Resources.PeakMemoryMiB
	percent := m.config.GPUReservationPercent
	if !model.Resources.GPURequired || percent <= 100 {
		return peak
	}
	// Saturating arithmetic fails oversized declarations closed at admission.
	if peak > (math.MaxInt-99)/percent {
		return math.MaxInt
	}
	return (peak*percent + 99) / 100
}

// Saturate memory ledger totals instead of wrapping into negative capacity.
// Metadata and configuration validation ensure both operands are non-negative.
func addMemory(a, b int) int {
	if b > math.MaxInt-a {
		return math.MaxInt
	}
	return a + b
}

func (m *Manager) usedLocked() int {
	n := 0
	for _, e := range m.models {
		n = addMemory(n, e.reserved)
	}
	return n
}
func (m *Manager) capacity() int {
	if len(m.config.GPUBudgetsMiB) == 0 {
		return m.config.BudgetMiB - m.config.SafetyMarginMiB
	}
	total := 0
	for uuid := range m.config.GPUBudgetsMiB {
		total = addMemory(total, m.deviceCapacity(uuid))
	}
	return total
}
func (m *Manager) deviceCapacity(uuid string) int {
	if len(m.config.GPUBudgetsMiB) == 0 {
		return m.capacity()
	}
	budget, ok := m.config.GPUBudgetsMiB[uuid]
	if !ok {
		return 0
	}
	return budget - m.config.SafetyMarginMiB
}
func (m *Manager) deviceUsedLocked(uuid string) int {
	if len(m.config.GPUBudgetsMiB) == 0 {
		return m.usedLocked()
	}
	total := 0
	for _, e := range m.models {
		if e.model.GPUUUID == uuid {
			total = addMemory(total, e.reserved)
		}
	}
	return total
}
func (m *Manager) validatePlacement(model Model) error {
	if !model.Resources.GPURequired || len(m.config.GPUBudgetsMiB) == 0 {
		return nil
	}
	if _, ok := m.config.GPUBudgetsMiB[model.GPUUUID]; !ok {
		return fmt.Errorf("model %s must expose exactly one configured physical GPU UUID; recreate legacy all-GPU containers before enabling per-GPU admission", model.Name)
	}
	return nil
}

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
				e.reserved = m.peakReservation(model)
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
				e.reserved = m.residentReservation(model)
				if runtime.LastUsedAt != nil {
					e.lastUsed = *runtime.LastUsedAt
				} else {
					e.lastUsed = time.Now()
				}
			} else {
				e.state = types.RuntimeError
				e.reserved = m.peakReservation(model)
				quarantined = true
			}
		}
		if e.reserved > 0 {
			if err := m.validatePlacement(model); err != nil {
				outcome = "failed"
				return err
			}
		}
		staged[id] = e
	}
	if m.shedOverCapacity(ctx, staged) {
		quarantined = true
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

// shedOverCapacity makes a reconstructed ledger fit its devices. Loaded models
// can add up to more than a budget after a restart: reservations were measured
// and are declared again, the stored measurements were lost, or a peak grew
// past what was free. Failing closed would stop all admission for memory that
// is merely idle, so idle models are unloaded, least recently used first, until
// each device fits. A model that cannot be unloaded keeps its reservation in
// ERROR; the result reports whether any device is still over its budget, in
// which case that device admits nothing until the quarantine is released.
func (m *Manager) shedOverCapacity(ctx context.Context, staged map[string]*entry) bool {
	pools := map[string][]*entry{}
	used := map[string]int{}
	for _, e := range staged {
		pool := e.model.GPUUUID
		if len(m.config.GPUBudgetsMiB) == 0 {
			pool = ""
		}
		if e.reserved > 0 {
			pools[pool] = append(pools[pool], e)
			used[pool] = addMemory(used[pool], e.reserved)
		}
	}
	over := false // also set when an unload failed and left a quarantined model
	for pool, entries := range pools {
		capacity := m.deviceCapacity(pool)
		if used[pool] <= capacity {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			if !entries[i].lastUsed.Equal(entries[j].lastUsed) {
				return entries[i].lastUsed.Before(entries[j].lastUsed)
			}
			return entries[i].model.ContainerID < entries[j].model.ContainerID
		})
		for _, e := range entries {
			if used[pool] <= capacity || ctx.Err() != nil {
				break
			}
			if e.state != types.RuntimeReady || !e.loaded {
				continue
			}
			unloadCtx, cancel := context.WithTimeout(ctx, m.config.OperationTimeout)
			err := m.backend.Unload(unloadCtx, e.model)
			cancel()
			m.metrics.ObserveEvent("reconciliation_eviction", metricOutcome(err))
			if err != nil {
				e.state = types.RuntimeError
				over = true
				continue
			}
			used[pool] -= e.reserved
			e.reserved, e.loaded, e.state = 0, false, types.RuntimeUnloaded
		}
		if used[pool] > capacity {
			over = true
			m.metrics.ObserveEvent("reconciliation", "over_capacity")
		}
	}
	return over
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
	if err = m.validatePlacement(model); err != nil {
		return response, err
	}
	m.mu.Lock()
	if e.reserved > 0 && (e.model.Resources != model.Resources || e.model.GPUUUID != model.GPUUUID) {
		m.mu.Unlock()
		return response, errors.New("resident model resource contract changed")
	}
	unaccounted := e.reserved == 0 && !e.leased
	e.model = model
	// Discovery is serialized with admission. Until runtime is known, retain a
	// conservative peak reservation; a failed runtime probe must not free it.
	if unaccounted {
		e.reserved = m.peakReservation(model)
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
		e.reserved = m.peakReservation(model)
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
	// A request waiting for one physical device must not block preparation on
	// another device. Reservations remain atomic under m.mu.
	if len(m.config.GPUBudgetsMiB) > 0 {
		m.mu.Lock()
		if m.admitting == w {
			m.admitting = nil
		}
		m.wakeLocked()
		m.mu.Unlock()
	}
	if err = m.reserve(queueCtx, e); err != nil {
		m.metrics.ObserveEvent("admission_failure", metricOutcome(err))
		return response, queueError(ctx, queueCtx, err)
	}
	// Admission is finished; other models may now load concurrently within budget.
	m.mu.Lock()
	if m.admitting == w {
		m.admitting = nil
	}
	m.releaseResidentAdmissionLocked(w)
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
	e.busySince, e.busySamples, e.sampledActive = time.Now(), 0, false
	m.wakeLocked()
	m.mu.Unlock()
	// Ask for a sample of this GPU once the prediction is well under way.
	defer m.kickMeasurementAfter(model.GPUUUID, m.config.MeasuredMinBusy)()
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
	if m.models[id].blocked {
		m.mu.Unlock()
		return nil, nil, &AdmissionBlocked{}
	}
	w := &waiter{id: id}
	m.queue = append(m.queue, w)
	m.metrics.SetSnapshot(m.snapshotLocked())
	for {
		if m.models[id].blocked {
			m.removeLocked(w)
			m.wakeLocked()
			m.mu.Unlock()
			return nil, nil, &AdmissionBlocked{}
		}
		if err := ctx.Err(); err != nil {
			m.removeLocked(w)
			m.wakeLocked()
			m.mu.Unlock()
			return nil, nil, err
		}
		var first *waiter
		for _, candidate := range m.queue {
			e := m.models[candidate.id]
			// A resident waiting to grow to peak must leave other residents on
			// its device idle and evictable. Cold requests hold no residency.
			deviceBusy := len(m.config.GPUBudgetsMiB) > 0 && e.reserved > 0 && m.residentAdmission[e.model.GPUUUID] != nil
			if !e.active && !deviceBusy {
				first = candidate
				break
			}
		}
		if m.admitting == nil && first == w {
			m.removeLocked(w)
			e := m.models[id]
			e.active = true
			e.sampledActive = false
			if len(m.config.GPUBudgetsMiB) > 0 && e.reserved > 0 {
				m.residentAdmission[e.model.GPUUUID] = w
			}
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

// BlockAndDrain closes admission for one registered container, waits for
// admitted work to finish, and confirms that its model is unloaded. The gate
// stays closed until the upgrade controller explicitly calls Unblock.
func (m *Manager) BlockAndDrain(ctx context.Context, id string) (err error) {
	return m.blockAndDrain(ctx, id, false)
}

// BlockAndDrainIdle is BlockAndDrain for automatic placement: it refuses with
// ErrModelBusy, leaving admission open, when the model has an admitted request
// or one waiting, or is loaded and was used within the placement idle period
// (a request may have come and gone while the move was being prepared). The
// check and the gate are taken under one lock, so a request cannot slip in
// between them.
func (m *Manager) BlockAndDrainIdle(ctx context.Context, id string) error {
	return m.blockAndDrain(ctx, id, true)
}

// usedRecentlyLocked applies the automatic placement idle rule at the moment
// admission closes: a loaded model must have been idle for
// max(PlacementMinIdle, MinResidency). Callers hold mu.
func (m *Manager) usedRecentlyLocked(e *entry) bool {
	return e.loaded && !e.lastUsed.IsZero() && time.Since(e.lastUsed) < max(m.config.PlacementMinIdle, m.config.MinResidency)
}

func (m *Manager) blockAndDrain(ctx context.Context, id string, requireIdle bool) (err error) {
	if !m.config.Enabled {
		return ErrDisabled
	}
	if !containerIDPattern.MatchString(id) {
		return errors.New("a full registered Docker container ID is required")
	}

	m.mu.Lock()
	if !m.ready || m.reconciling {
		m.mu.Unlock()
		return ErrNotReconciled
	}
	e, ok := m.models[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("registered model is absent from the manager inventory")
	}
	if e.draining {
		m.mu.Unlock()
		return errors.New("model drain is already in progress")
	}
	if requireIdle && (e.active || e.leased || m.hasQueuedLocked(id) || m.usedRecentlyLocked(e)) {
		m.mu.Unlock()
		m.metrics.ObserveEvent("drain", "refused_busy")
		return ErrModelBusy
	}
	e.blocked = true
	e.draining = true
	m.wakeLocked()

	for e.active || m.hasQueuedLocked(id) {
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			m.mu.Lock()
			e.draining = false
			m.wakeLocked()
			m.mu.Unlock()
			return ctx.Err()
		case <-changed:
		}
		m.mu.Lock()
	}
	if err = ctx.Err(); err != nil {
		e.draining = false
		m.wakeLocked()
		m.mu.Unlock()
		return err
	}

	e.active = true
	model := e.model
	if model.ContainerID == "" {
		model.ContainerID = id
	}
	needsUnload := e.loaded || e.reserved > 0 || e.leased
	if needsUnload {
		e.state = types.RuntimeEvicting
	}
	m.wakeLocked()
	m.mu.Unlock()

	started := time.Now()
	defer func() {
		p := recover()
		m.mu.Lock()
		e.active = false
		e.draining = false
		if err == nil && p == nil {
			e.reserved = 0
			e.leased = false
			e.loaded = false
			e.state = types.RuntimeUnloaded
		} else {
			e.state = types.RuntimeError
		}
		m.wakeLocked()
		m.mu.Unlock()
		outcome := metricOutcome(err)
		if p != nil {
			outcome = "panic"
		}
		m.metrics.ObserveOperation("drain", outcome, time.Since(started))
		m.metrics.ObserveEvent("drain", outcome)
		if p != nil {
			panic(p)
		}
	}()
	if needsUnload {
		err = m.backend.Unload(ctx, model)
	}
	return err
}

func (m *Manager) hasQueuedLocked(id string) bool {
	for _, queued := range m.queue {
		if queued.id == id {
			return true
		}
	}
	return false
}

// ReplaceDrained creates a closed slot for a candidate while preserving the
// old ID as a blocked tombstone. Stale requests cannot reload the old image.
func (m *Manager) ReplaceDrained(oldID, newID string) error {
	if !m.config.Enabled {
		return ErrDisabled
	}
	if !containerIDPattern.MatchString(oldID) || !containerIDPattern.MatchString(newID) {
		return errors.New("full Docker container IDs are required")
	}
	if oldID == newID {
		return errors.New("candidate container must differ from the drained container")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.reconciling {
		return ErrNotReconciled
	}
	old, ok := m.models[oldID]
	if !ok || !old.blocked || old.draining || old.preparing || old.active || old.loaded || old.reserved != 0 || old.leased || m.hasQueuedLocked(oldID) {
		return errors.New("old model must be blocked and fully drained before replacement")
	}
	if destination, exists := m.models[newID]; exists {
		if !destination.blocked || destination.draining || destination.preparing || destination.active || destination.loaded || destination.reserved != 0 || destination.leased || m.hasQueuedLocked(newID) {
			return errors.New("existing candidate slot must be blocked and fully drained")
		}
		destination.model = Model{ContainerID: newID}
		destination.state = types.RuntimeUnloaded
	} else {
		m.models[newID] = &entry{
			model:   Model{ContainerID: newID},
			state:   types.RuntimeUnloaded,
			blocked: true,
		}
	}
	m.measMu.Lock()
	m.replaces[newID] = oldID
	m.measMu.Unlock()
	m.wakeLocked()
	return nil
}

// PrepareBlocked starts and loads a candidate through the normal capacity
// ledger without opening it to inference traffic.
func (m *Manager) PrepareBlocked(ctx context.Context, id string) (err error) {
	if !m.config.Enabled {
		return ErrDisabled
	}
	if !containerIDPattern.MatchString(id) {
		return errors.New("a full registered Docker container ID is required")
	}
	m.mu.Lock()
	if !m.ready || m.reconciling {
		m.mu.Unlock()
		return ErrNotReconciled
	}
	e, ok := m.models[id]
	if !ok || !e.blocked || e.draining || e.preparing || e.active || e.loaded || e.reserved != 0 || e.leased || e.state == types.RuntimeError {
		m.mu.Unlock()
		return errors.New("candidate must be registered, blocked, healthy, and fully drained")
	}
	e.active = true
	e.preparing = true
	m.wakeLocked()
	m.mu.Unlock()

	var model Model
	defer func() {
		p := recover()
		cleanupConfirmed := true
		if (err != nil || p != nil) && model.ContainerID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.config.OperationTimeout)
			cleanupErr := m.backend.Unload(cleanupCtx, model)
			cancel()
			if cleanupErr != nil {
				cleanupConfirmed = false
				err = errors.Join(err, fmt.Errorf("candidate cleanup could not be confirmed: %w", cleanupErr))
			}
		}
		m.mu.Lock()
		e.active = false
		e.preparing = false
		if err != nil || p != nil {
			e.state = types.RuntimeError
			if cleanupConfirmed {
				e.reserved = 0
				e.leased = false
				e.loaded = false
			} else {
				e.model = model
				if m.validatePlacement(model) != nil {
					m.ready = false
				}
				e.reserved = max(e.reserved, m.peakReservation(model))
				if e.reserved == 0 {
					e.reserved = m.deviceCapacity(model.GPUUUID)
				}
				e.leased = true
				e.loaded = true
			}
		} else {
			e.reserved = m.residentReservation(e.model)
			e.leased = false
			e.loaded = true
			e.state = types.RuntimeReady
			e.lastUsed = time.Now()
		}
		m.wakeLocked()
		m.mu.Unlock()
		if p != nil {
			panic(p)
		}
	}()

	model, err = m.backend.Prepare(ctx, id)
	if err != nil {
		return err
	}
	if model.ContainerID != id {
		return errors.New("lifecycle backend returned a different container")
	}
	if err = model.Resources.Validate(); err != nil {
		return err
	}
	if err = m.validatePlacement(model); err != nil {
		return err
	}
	m.mu.Lock()
	e.model = model
	m.inheritMeasurementLocked(model)
	m.mu.Unlock()
	if err = m.reserve(ctx, e); err != nil {
		return err
	}
	runtime, runtimeErr := m.backend.Runtime(ctx, model)
	if runtimeErr != nil {
		return runtimeErr
	}
	if err = runtime.Validate(); err != nil {
		return err
	}
	if runtime.ActiveRequests != 0 || (runtime.State != types.RuntimeReady && runtime.State != types.RuntimeUnloaded) {
		return errors.New("candidate runtime is not idle")
	}
	if !runtime.Loaded {
		if err = m.backend.Load(ctx, model); err != nil {
			return err
		}
	}
	return nil
}

// Unblock reopens admission after activation or after a drain was canceled.
func (m *Manager) Unblock(id string) error {
	if !containerIDPattern.MatchString(id) {
		return errors.New("a full registered Docker container ID is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.reconciling {
		return ErrNotReconciled
	}
	e, ok := m.models[id]
	if !ok {
		return errors.New("model is absent from the manager inventory")
	}
	if e.draining {
		return errors.New("model drain is still in progress")
	}
	if e.preparing {
		return errors.New("candidate preparation is still in progress")
	}
	if e.state == types.RuntimeError {
		return errors.New("model is not safe to admit")
	}
	e.blocked = false
	m.wakeLocked()
	return nil
}

// HoldAdmissionForRename runs rename while no request is admitted to the model:
// it waits for admitted work to finish, then claims the model the way an
// eviction does, so requests that arrive meanwhile wait in the queue instead of
// failing. The model stays loaded and its reservation is untouched. When rename
// succeeds, the name it returns is the one the container is addressed by from
// then on. The gateway reaches a model container by its Docker name, so a
// rename outside this hold could fail a request already on its way.
func (m *Manager) HoldAdmissionForRename(ctx context.Context, id string, rename func(context.Context) (string, error)) (err error) {
	if !m.config.Enabled {
		return ErrDisabled
	}
	if !containerIDPattern.MatchString(id) {
		return errors.New("a full registered Docker container ID is required")
	}
	if rename == nil {
		return errors.New("a rename operation is required")
	}
	m.mu.Lock()
	var e *entry
	for {
		if !m.ready || m.reconciling {
			m.mu.Unlock()
			return ErrNotReconciled
		}
		current, ok := m.models[id]
		if !ok {
			m.mu.Unlock()
			return errors.New("model is absent from the manager inventory")
		}
		if !current.active && !current.draining && !current.preparing {
			e = current
			break
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		m.mu.Lock()
	}
	e.active = true
	m.wakeLocked()
	m.mu.Unlock()

	name := ""
	defer func() {
		p := recover()
		m.mu.Lock()
		e.active = false
		if name = strings.TrimPrefix(strings.TrimSpace(name), "/"); err == nil && p == nil && name != "" {
			e.model.Name = name
		}
		m.wakeLocked()
		m.mu.Unlock()
		if p != nil {
			panic(p)
		}
	}()
	name, err = rename(ctx)
	return err
}

func (m *Manager) removeLocked(w *waiter) {
	for i, v := range m.queue {
		if v == w {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

// queuedAdmissibleLocked reports whether a request queued for this idle
// resident could be admitted while the caller waits for memory. Only then is it
// worth keeping the model loaded for that request. When the request is held
// back by an admission the caller owns (the global admission slot, or the
// per-GPU resident slot of the model's device), it cannot run until the caller
// gets its memory, so protecting the model would stall both requests until the
// queue timeout.
func (m *Manager) queuedAdmissibleLocked(id string, e *entry) bool {
	if !m.hasQueuedLocked(id) || m.admitting != nil {
		return false
	}
	deviceBusy := len(m.config.GPUBudgetsMiB) > 0 && e.reserved > 0 && m.residentAdmission[e.model.GPUUUID] != nil
	return !deviceBusy
}

func (m *Manager) releaseResidentAdmissionLocked(w *waiter) {
	for uuid, owner := range m.residentAdmission {
		if owner == w {
			delete(m.residentAdmission, uuid)
		}
	}
}

func (m *Manager) reserve(ctx context.Context, target *entry) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		if !m.ready || m.reconciling {
			m.mu.Unlock()
			return ErrNotReconciled
		}
		peak := m.peakReservation(target.model)
		capacity := m.deviceCapacity(target.model.GPUUUID)
		if peak > capacity {
			m.mu.Unlock()
			return ErrCapacity
		}
		if peak-target.reserved <= capacity-m.deviceUsedLocked(target.model.GPUUUID) {
			target.reserved = peak
			target.leased = true
			target.state = types.RuntimeLoading
			m.wakeLocked()
			m.mu.Unlock()
			return nil
		}
		now := time.Now()
		// Before making this request wait or unload a neighbour, make sure the
		// memory it lacks is not held for a container that no longer exists.
		if m.ghostSweepDueLocked(now) {
			m.mu.Unlock()
			m.dropGhostReservations(ctx)
			continue
		}
		var victim *entry
		wait := m.config.QueueTimeout
		for id, e := range m.models {
			if e == target || e.active || e.blocked || e.state != types.RuntimeReady || e.reserved == 0 || (len(m.config.GPUBudgetsMiB) > 0 && e.model.GPUUUID != target.model.GPUUUID) {
				continue
			}
			// A model stays resident for its idle timeout, or under the
			// on-demand policy only for the minimum residency, so a burst of
			// different models is not held up by ones that already answered.
			keep := time.Duration(e.model.Resources.IdleTimeoutSeconds) * time.Second
			if m.config.EvictionPolicy == EvictionLRUOnDemand {
				if m.queuedAdmissibleLocked(id, e) {
					continue
				}
				keep = m.config.MinResidency
			}
			remaining := keep - now.Sub(e.lastUsed)
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
			// A failed eviction quarantines that model's reservation (see
			// evict). The request then looks for another victim or waits; it
			// does not fail because one neighbour could not be unloaded.
			_ = m.evict(ctx, victim, model)
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
			m.countObservedPredictionLocked(e)
			e.reserved = m.residentReservation(e.model)
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
		e.sampledActive = false
		if m.admitting == w {
			m.admitting = nil
		}
		m.releaseResidentAdmissionLocked(w)
		m.wakeLocked()
		m.mu.Unlock()
		m.kickMeasurement(model.GPUUUID)
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
	GPUUUID     string
	State       types.ModelRuntimeState
	ReservedMiB int
	Active      bool
	LastUsedAt  time.Time
	QueueDepth  int
	Loaded      bool
	Blocked     bool
	Draining    bool
	Preparing   bool
}
type Snapshot struct {
	Ready                                 bool
	CapacityMiB, ResidentMiB, ActiveMiB   int
	ReservedMiB, AvailableMiB, QueueDepth int
	Models                                map[string]ModelSnapshot
	GPUs                                  map[string]GPUSnapshot
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() Snapshot {
	s := Snapshot{Ready: m.ready && !m.reconciling, CapacityMiB: m.capacity(), ReservedMiB: m.usedLocked(), Models: make(map[string]ModelSnapshot)}
	for id, e := range m.models {
		s.Models[id] = ModelSnapshot{
			State: e.state, ReservedMiB: e.reserved, Active: e.active, GPUUUID: e.model.GPUUUID,
			LastUsedAt: e.lastUsed, Loaded: e.loaded, Blocked: e.blocked,
			Draining: e.draining, Preparing: e.preparing,
		}
		resident := 0
		if e.loaded && e.model.Resources.GPURequired {
			resident = min(e.reserved, m.residentReservation(e.model))
		}
		s.ResidentMiB = addMemory(s.ResidentMiB, resident)
		s.ActiveMiB = addMemory(s.ActiveMiB, e.reserved-resident)
	}
	for _, w := range m.queue {
		v := s.Models[w.id]
		v.QueueDepth++
		s.Models[w.id] = v
		s.QueueDepth++
	}
	s.GPUs = make(map[string]GPUSnapshot)
	for uuid := range m.config.GPUBudgetsMiB {
		gpu := GPUSnapshot{CapacityMiB: m.deviceCapacity(uuid), ReservedMiB: m.deviceUsedLocked(uuid)}
		for _, e := range m.models {
			if e.model.GPUUUID == uuid && e.loaded {
				gpu.ResidentMiB = addMemory(gpu.ResidentMiB, min(e.reserved, m.residentReservation(e.model)))
			}
		}
		gpu.ActiveMiB = gpu.ReservedMiB - gpu.ResidentMiB
		gpu.AvailableMiB = max(0, gpu.CapacityMiB-gpu.ReservedMiB)
		s.GPUs[uuid] = gpu
	}
	s.AvailableMiB = max(0, s.CapacityMiB-s.ReservedMiB)
	return s
}

// GPUSnapshot is a reservation ledger for one physical device, not live NVML usage.
type GPUSnapshot struct{ CapacityMiB, ReservedMiB, ResidentMiB, ActiveMiB, AvailableMiB int }

// ValidateGPUPlacement checks an explicit administrative target before deployment.
func (m *Manager) ValidateGPUPlacement(uuid string, resources types.ModelResources) error {
	if len(m.config.GPUBudgetsMiB) == 0 {
		return errors.New("explicit GPU placement requires per-device budgets")
	}
	return m.ValidateGPUPreference(uuid, resources)
}

// ValidateGPUPreference preserves a registration's existing UUID pin with the
// legacy shared budget. Explicit administrative moves still require per-device
// budgets through ValidateGPUPlacement.
func (m *Manager) ValidateGPUPreference(uuid string, resources types.ModelResources) error {
	if err := resources.Validate(); err != nil {
		return err
	}
	if !resources.GPURequired || !gpuUUIDPattern.MatchString(uuid) {
		return errors.New("GPU preference requires a GPU model and physical GPU UUID")
	}
	if _, ok := m.config.GPUBudgetsMiB[uuid]; len(m.config.GPUBudgetsMiB) > 0 && !ok {
		return errors.New("GPU is not configured for managed inference")
	}
	if m.peakReservation(Model{Resources: resources}) > m.deviceCapacity(uuid) {
		return ErrCapacity
	}
	return nil
}
