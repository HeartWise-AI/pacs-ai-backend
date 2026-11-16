package modelmanager

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
)

const (
	// measuredHeadroomMiB is added to every measured reservation on top of the
	// safety percentage, so small models keep an absolute allowance.
	measuredHeadroomMiB = 256
	// A GPU with nothing loaded is still sampled for foreign processes, but slowly.
	idleGPUSampleInterval = 30 * time.Second
	// A process that belongs to no managed container is looked up again at this
	// pace, in case it was listed before its container reported it.
	foreignRecheckInterval = 30 * time.Second
	measurementSaveGap     = 5 * time.Second
	// kickGap bounds the extra samples predictions may request for one GPU.
	kickGap = time.Second
	// A GPU whose probe fails is retried with a doubling delay up to this.
	maxProbeBackoff     = 5 * time.Minute
	defaultReadTimeout  = 5 * time.Second
	defaultOwnerTimeout = 3 * time.Second
)

// GPUProbe is the read-only Docker surface of the sampler. Both calls report
// host PIDs, so a GPU process can be matched to the container that owns it.
type GPUProbe interface {
	ReadGPUProcesses(ctx context.Context, containerID, gpuUUID string) ([]dockerSDK.GPUProcess, error)
	ContainerProcessIDs(ctx context.Context, containerID string) ([]string, error)
}

// Measurement is what has been observed for one model container. PeakMiB is
// the high-water mark of every sample taken while the model was loading,
// predicting or loaded; ResidentMiB of the samples taken while loaded and idle.
type Measurement struct {
	ContainerID string    `json:"containerId"`
	Name        string    `json:"name,omitempty"`
	ImageID     string    `json:"imageId,omitempty"`
	GPUUUID     string    `json:"gpuUuid,omitempty"`
	LastMiB     int       `json:"lastMiB"`
	PeakMiB     int       `json:"peakMiB"`
	ResidentMiB int       `json:"residentMiB"`
	Samples     int       `json:"samples"` // completed predictions observed
	Overshoots  int       `json:"overshoots"`
	SampledAt   time.Time `json:"sampledAt"`
}

// MeasurementStore persists observations across gateway restarts.
type MeasurementStore interface {
	Load(ctx context.Context) ([]Measurement, error)
	Save(ctx context.Context, upsert []Measurement, remove []string) error
}

type measurement struct {
	Measurement
	dirty bool
}

// GPUUsage is the last per-process sample of one device. UnattributedMiB is
// memory of processes whose owner could not be determined in that sample; it is
// neither assigned to a model nor reported as foreign. A zero SampledAt means
// the device was never sampled.
type GPUUsage struct {
	ManagedMiB, ForeignMiB, UnattributedMiB int
	SampledAt                               time.Time
}

// ModelMeasurement joins the reservation the manager would make now with what
// was measured. MeasuredReservationMiB is the value the measured source yields
// (zero until enough predictions were observed) regardless of the active source.
type ModelMeasurement struct {
	ContainerID, GPUUUID   string
	ResourcesKnown         bool
	GPURequired            bool
	DeclaredPeakMiB        int
	DeclaredReservationMiB int
	ReservationMiB         int
	ResidentReservationMiB int
	MeasuredReservationMiB int
	ReservationSource      string
	Sampled                bool
	MeasuredPeakMiB        int
	MeasuredResidentMiB    int
	LastMiB                int
	Samples                int
	Overshoots             int
	SampledAt              time.Time
}

type MeasurementReport struct {
	ReservationSource string
	MinSamples        int
	Models            map[string]ModelMeasurement
	GPUs              map[string]GPUUsage
}

func (m *Manager) scaleMeasured(value int) int {
	percent := m.config.MeasuredSafetyPercent
	if value > (math.MaxInt-99-measuredHeadroomMiB*100)/percent {
		return math.MaxInt
	}
	return (value*percent+99)/100 + measuredHeadroomMiB
}

// measuredCandidate returns the measured peak and resident reservations before
// the declared ceiling is applied, once enough predictions were observed.
func (m *Manager) measuredCandidate(model Model) (peak, resident int, ok bool) {
	if !model.Resources.GPURequired || model.ContainerID == "" {
		return 0, 0, false
	}
	m.measMu.Lock()
	defer m.measMu.Unlock()
	observed := m.measurements[model.ContainerID]
	if observed == nil || observed.Samples < m.config.MeasuredMinSamples || observed.PeakMiB <= 0 {
		return 0, 0, false
	}
	peak = m.scaleMeasured(observed.PeakMiB)
	if observed.ResidentMiB > 0 {
		// The idle footprint gets the same margin: the peak reservation is
		// never below what the model is known to keep while loaded.
		resident = m.scaleMeasured(observed.ResidentMiB)
		peak = max(peak, resident)
	}
	return peak, resident, true
}

func (m *Manager) measuredPeak(model Model) (int, bool) {
	if m.config.ReservationSource != ReservationMeasured {
		return 0, false
	}
	peak, _, ok := m.measuredCandidate(model)
	return peak, ok
}

func (m *Manager) measuredResident(model Model) (int, bool) {
	if m.config.ReservationSource != ReservationMeasured {
		return 0, false
	}
	_, resident, ok := m.measuredCandidate(model)
	return resident, ok && resident > 0
}

// kickMeasurement asks the sampler for an extra sample of one GPU. It never blocks.
func (m *Manager) kickMeasurement(gpu string) {
	if gpu == "" || m.config.MeasureInterval <= 0 {
		return
	}
	m.measMu.Lock()
	m.kicked[gpu] = struct{}{}
	m.measMu.Unlock()
	select {
	case m.measureKick <- struct{}{}:
	default:
	}
}

// kickMeasurementAfter schedules that request and returns its cancellation.
func (m *Manager) kickMeasurementAfter(gpu string, delay time.Duration) func() {
	if gpu == "" || m.config.MeasureInterval <= 0 {
		return func() {}
	}
	timer := time.AfterFunc(delay, func() { m.kickMeasurement(gpu) })
	return func() { timer.Stop() }
}

// measurementLocked returns the record of one container. Callers hold mu.
func (m *Manager) measurementLocked(id string) *measurement {
	m.measMu.Lock()
	defer m.measMu.Unlock()
	observed := m.measurements[id]
	if observed == nil {
		observed = &measurement{Measurement: Measurement{ContainerID: id}}
		m.measurements[id] = observed
	}
	return observed
}

// countObservedPredictionLocked runs when a prediction completes successfully.
func (m *Manager) countObservedPredictionLocked(e *entry) {
	if !e.sampledActive || e.model.ContainerID == "" {
		return
	}
	observed := m.measurementLocked(e.model.ContainerID)
	m.measMu.Lock()
	observed.Samples++
	observed.dirty = true
	m.measMu.Unlock()
}

// inheritMeasurementLocked carries observations to the container that replaces
// another one, but only for the same image: a move between GPUs keeps what was
// learned, an upgrade to another image starts again from its declaration.
func (m *Manager) inheritMeasurementLocked(model Model) {
	m.measMu.Lock()
	defer m.measMu.Unlock()
	oldID, ok := m.replaces[model.ContainerID]
	if !ok {
		return
	}
	delete(m.replaces, model.ContainerID)
	source := m.measurements[oldID]
	if source == nil || source.ImageID == "" || source.ImageID != model.ImageID || m.measurements[model.ContainerID] != nil {
		return
	}
	m.measurements[model.ContainerID] = &measurement{dirty: true, Measurement: Measurement{
		ContainerID: model.ContainerID, Name: model.Name, ImageID: model.ImageID, GPUUUID: model.GPUUUID,
		PeakMiB: source.PeakMiB, ResidentMiB: source.ResidentMiB, Samples: source.Samples,
	}}
}

// LoadMeasurements restores persisted observations. Call it before the first
// reconciliation so that measured reservations apply to models already loaded.
func (m *Manager) LoadMeasurements(ctx context.Context, store MeasurementStore) error {
	if store == nil {
		return nil
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.measMu.Lock()
	defer m.measMu.Unlock()
	for _, item := range loaded {
		if !containerIDPattern.MatchString(item.ContainerID) || item.PeakMiB < 0 || item.ResidentMiB < 0 || item.Samples < 0 || item.Overshoots < 0 {
			continue
		}
		if _, exists := m.measurements[item.ContainerID]; !exists {
			m.measurements[item.ContainerID] = &measurement{Measurement: item}
		}
	}
	return nil
}

// Settings returns the validated configuration with defaults applied.
func (m *Manager) Settings() Config { return m.config }

// ObservePlacementMove counts one automatic GPU move request.
func (m *Manager) ObservePlacementMove(accepted bool) {
	outcome := "refused"
	if accepted {
		outcome = "accepted"
	}
	m.metrics.ObserveEvent("placement_move", outcome)
}

// Measurements reports reservations and observations for every known model.
func (m *Manager) Measurements() MeasurementReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.measurementsLocked()
}

func (m *Manager) measurementsLocked() MeasurementReport {
	report := MeasurementReport{ReservationSource: m.config.ReservationSource, MinSamples: m.config.MeasuredMinSamples, Models: make(map[string]ModelMeasurement, len(m.models)), GPUs: make(map[string]GPUUsage)}
	for id, e := range m.models {
		model := e.model
		model.ContainerID = id
		report.Models[id] = m.describe(model, model.Resources.Validate() == nil && e.model.ContainerID != "")
	}
	for uuid := range m.config.GPUBudgetsMiB {
		report.GPUs[uuid] = GPUUsage{}
	}
	m.measMu.Lock()
	for uuid, usage := range m.gpuUsage {
		report.GPUs[uuid] = usage
	}
	m.measMu.Unlock()
	return report
}

// ModelReservation describes one container using resources read by the caller,
// for a model the manager has not prepared yet.
func (m *Manager) ModelReservation(containerID, gpuUUID string, resources types.ModelResources) ModelMeasurement {
	return m.describe(Model{ContainerID: containerID, GPUUUID: gpuUUID, Resources: resources}, resources.Validate() == nil)
}

func (m *Manager) describe(model Model, known bool) ModelMeasurement {
	view := ModelMeasurement{ContainerID: model.ContainerID, GPUUUID: model.GPUUUID, ResourcesKnown: known, ReservationSource: ReservationDeclared}
	if known {
		view.GPURequired = model.Resources.GPURequired
		view.DeclaredPeakMiB = model.Resources.PeakMemoryMiB
		view.DeclaredReservationMiB = m.declaredReservation(model)
		view.ReservationMiB = m.peakReservation(model)
		view.ResidentReservationMiB = m.residentReservation(model)
		if peak, _, ok := m.measuredCandidate(model); ok {
			view.MeasuredReservationMiB = min(peak, view.DeclaredReservationMiB)
			if m.config.ReservationSource == ReservationMeasured {
				view.ReservationSource = ReservationMeasured
			}
		}
	}
	m.measMu.Lock()
	defer m.measMu.Unlock()
	if observed := m.measurements[model.ContainerID]; observed != nil {
		view.Sampled = !observed.SampledAt.IsZero()
		view.MeasuredPeakMiB, view.MeasuredResidentMiB, view.LastMiB = observed.PeakMiB, observed.ResidentMiB, observed.LastMiB
		view.Samples, view.Overshoots, view.SampledAt = observed.Samples, observed.Overshoots, observed.SampledAt
	}
	return view
}

type observation struct {
	id, gpu string
	at      time.Time
	live    bool // loading, predicting, loaded or otherwise holding a lease
	idle    bool
	// predicting is true while the model's Predict call is running; busySince
	// identifies that prediction.
	predicting bool
	busySince  time.Time
	lastUsed   time.Time
}

func (m *Manager) observeLocked() map[string]observation {
	observed := make(map[string]observation, len(m.models))
	at := time.Now()
	for id, e := range m.models {
		observed[id] = observation{
			id: id, gpu: e.model.GPUUUID, at: at, lastUsed: e.lastUsed, busySince: e.busySince,
			live:       e.loaded || e.active || e.leased,
			idle:       e.loaded && !e.active && !e.leased && e.state == types.RuntimeReady,
			predicting: e.active && e.state == types.RuntimeBusy,
		}
	}
	return observed
}

// gpuSampler owns the state of the measurement loop. It is used by a single
// goroutine; everything shared with admission lives in the Manager.
type gpuSampler struct {
	manager      *Manager
	probe        GPUProbe
	store        MeasurementStore
	now          func() time.Time
	readTimeout  time.Duration
	ownerTimeout time.Duration
	owners       map[string]map[string]string // GPU -> host PID -> container ID ("" is foreign)
	checked      map[string]time.Time         // foreign host PID -> last ownership lookup
	probeID      map[string]string            // GPU -> container that last answered
	attempted    map[string]time.Time
	failures     map[string]int // consecutive failed reads per GPU
	retryAt      map[string]time.Time
	lastSave     time.Time
	storeFailed  bool
}

func newGPUSampler(m *Manager, probe GPUProbe, store MeasurementStore) *gpuSampler {
	return &gpuSampler{
		manager: m, probe: probe, store: store, now: time.Now, readTimeout: defaultReadTimeout, ownerTimeout: defaultOwnerTimeout,
		owners: map[string]map[string]string{}, checked: map[string]time.Time{}, probeID: map[string]string{}, attempted: map[string]time.Time{},
		failures: map[string]int{}, retryAt: map[string]time.Time{},
	}
}

// RunGPUMeasurement samples per-process GPU memory until the context ends. It
// only reads: it never starts a container and never changes a model's state.
// Every Docker call it makes has its own deadline.
func (m *Manager) RunGPUMeasurement(ctx context.Context, probe GPUProbe, store MeasurementStore) {
	if !m.config.Enabled || m.config.MeasureInterval <= 0 || probe == nil {
		return
	}
	sampler := newGPUSampler(m, probe, store)
	ticker := time.NewTicker(m.config.MeasureInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampler.sample(ctx, nil)
		case <-m.measureKick:
			if only := sampler.takeKicked(); len(only) > 0 {
				sampler.sample(ctx, only)
			}
		}
	}
}

// takeKicked returns the GPUs predictions asked to sample that were not
// attempted within kickGap; the other requests are dropped, the next tick
// covers them.
func (s *gpuSampler) takeKicked() map[string]struct{} {
	m := s.manager
	m.measMu.Lock()
	kicked := m.kicked
	m.kicked = make(map[string]struct{})
	m.measMu.Unlock()
	now := s.now()
	for gpu := range kicked {
		if now.Sub(s.attempted[gpu]) < min(kickGap, m.config.MeasureInterval) {
			delete(kicked, gpu)
		}
	}
	return kicked
}

type gpuSample struct {
	processes []dockerSDK.GPUProcess
	owners    map[string]string
}

// sample reads the given GPUs, or every GPU when only is nil.
func (s *gpuSampler) sample(ctx context.Context, only map[string]struct{}) {
	m := s.manager
	m.mu.Lock()
	before := m.observeLocked()
	m.mu.Unlock()

	byGPU := map[string][]observation{}
	for _, item := range before {
		if _, wanted := only[item.gpu]; item.gpu != "" && (only == nil || wanted) {
			byGPU[item.gpu] = append(byGPU[item.gpu], item)
		}
	}
	now := s.now()
	samples := map[string]gpuSample{}
	for gpu, models := range byGPU {
		sort.Slice(models, func(i, j int) bool { return models[i].id < models[j].id })
		live := false
		for _, item := range models {
			live = live || item.live
		}
		if now.Before(s.retryAt[gpu]) || (!live && now.Sub(s.attempted[gpu]) < idleGPUSampleInterval) {
			continue
		}
		s.attempted[gpu] = now
		processes, ok := s.read(ctx, gpu, models)
		if !ok {
			// Keep the previous observations. Forget who owned which PID: by
			// the time the GPU answers again a PID may belong to another
			// process. Retry with a doubling delay so that a stuck driver is
			// not asked, and does not collect stuck processes, every cycle.
			delete(s.owners, gpu)
			s.failures[gpu]++
			delay := max(m.config.MeasureInterval, time.Second) << min(s.failures[gpu]-1, 16)
			s.retryAt[gpu] = now.Add(min(delay, maxProbeBackoff))
			m.metrics.ObserveEvent("gpu_sample", "unavailable")
			continue
		}
		s.failures[gpu] = 0
		delete(s.retryAt, gpu)
		m.metrics.ObserveEvent("gpu_sample", "success")
		samples[gpu] = gpuSample{processes: processes, owners: s.resolve(ctx, gpu, models, processes, now)}
	}
	s.apply(before, byGPU, samples, now)
	s.persist(ctx, now)
}

// read asks one running container pinned to the GPU, preferring the one that
// answered last and then the models known to be loaded. Only a container that
// is not running is skipped for the next one: a timeout or a driver error would
// repeat in every container, so the round ends there.
func (s *gpuSampler) read(ctx context.Context, gpu string, models []observation) ([]dockerSDK.GPUProcess, bool) {
	candidates := make([]string, 0, len(models)+1)
	if id := s.probeID[gpu]; id != "" {
		for _, item := range models {
			if item.id == id {
				candidates = append(candidates, id)
			}
		}
	}
	for _, wantLive := range []bool{true, false} {
		for _, item := range models {
			if item.live == wantLive && (len(candidates) == 0 || item.id != candidates[0]) {
				candidates = append(candidates, item.id)
			}
		}
	}
	for _, id := range candidates {
		if ctx.Err() != nil {
			break
		}
		readCtx, cancel := context.WithTimeout(ctx, s.readTimeout)
		processes, err := s.probe.ReadGPUProcesses(readCtx, id, gpu)
		cancel()
		if err == nil {
			s.probeID[gpu] = id
			return processes, true
		}
		if !errors.Is(err, dockerSDK.ErrGPUProbeNotRunning) {
			break
		}
	}
	delete(s.probeID, gpu)
	return nil, false
}

// resolve maps the host PIDs of one sample to model containers. Docker is only
// asked when a PID is new, so a steady state costs no process listing. A PID
// absent from the result could not be attributed in this round: a process
// listing failed for a container that may be running, so calling the PID
// foreign would be a guess. It is looked up again on the next sample.
func (s *gpuSampler) resolve(ctx context.Context, gpu string, models []observation, processes []dockerSDK.GPUProcess, now time.Time) map[string]string {
	previous := s.owners[gpu]
	owners := make(map[string]string, len(processes))
	lookup := false
	for _, process := range processes {
		owner, known := previous[process.PID]
		if known && (owner != "" || now.Sub(s.checked[process.PID]) < foreignRecheckInterval) {
			owners[process.PID] = owner
			continue
		}
		lookup = true
	}
	if lookup {
		containers := map[string]string{}
		uncertain := false
		for _, item := range models {
			if ctx.Err() != nil {
				uncertain = true
				break
			}
			ownerCtx, cancel := context.WithTimeout(ctx, s.ownerTimeout)
			pids, err := s.probe.ContainerProcessIDs(ownerCtx, item.id)
			cancel()
			if err != nil {
				// A stopped container owns no process; anything else is transient.
				uncertain = uncertain || !errors.Is(err, dockerSDK.ErrGPUProbeNotRunning)
				continue
			}
			for _, pid := range pids {
				containers[pid] = item.id
			}
		}
		for _, process := range processes {
			if _, done := owners[process.PID]; done {
				continue
			}
			owner, found := containers[process.PID]
			switch {
			case found:
				owners[process.PID] = owner
			case uncertain:
				// Keep what was known (a PID being re-checked stays foreign);
				// an unknown PID stays unattributed.
				if before, known := previous[process.PID]; known {
					owners[process.PID] = before
				}
			default:
				owners[process.PID] = ""
				s.checked[process.PID] = now
			}
		}
	}
	for pid := range s.checked {
		if now.Sub(s.checked[pid]) > 10*foreignRecheckInterval {
			delete(s.checked, pid)
		}
	}
	s.owners[gpu] = owners
	return owners
}

// apply records one round of samples. A sample only counts as idle or as
// in-prediction when the model was in that phase both before and after the
// Docker reads, so a prediction that ends mid-sample is never recorded as the
// model's idle footprint.
func (s *gpuSampler) apply(before map[string]observation, byGPU map[string][]observation, samples map[string]gpuSample, now time.Time) {
	m := s.manager
	var overshoots []string
	m.mu.Lock()
	after := m.observeLocked()
	wake := false
	for gpu, sample := range samples {
		perModel := map[string]int{}
		usage := GPUUsage{SampledAt: now}
		for _, process := range sample.processes {
			owner, attributed := sample.owners[process.PID]
			e, managed := m.models[owner]
			switch {
			case !attributed:
				usage.UnattributedMiB += process.UsedMemoryMiB
			case owner != "" && managed && e.model.GPUUUID == gpu:
				perModel[owner] += process.UsedMemoryMiB
				usage.ManagedMiB += process.UsedMemoryMiB
			default:
				usage.ForeignMiB += process.UsedMemoryMiB
			}
		}
		m.measMu.Lock()
		m.gpuUsage[gpu] = usage
		m.measMu.Unlock()
		for _, was := range byGPU[gpu] {
			e, ok := m.models[was.id]
			if !ok {
				continue
			}
			changed, overshoot := m.recordLocked(e, was, after[was.id], perModel[was.id], usage.UnattributedMiB == 0, now)
			wake = wake || changed
			if overshoot != "" {
				overshoots = append(overshoots, overshoot)
			}
		}
	}
	if m.ready && !m.reconciling {
		m.measMu.Lock()
		for id := range m.measurements {
			if _, registered := m.models[id]; !registered {
				delete(m.measurements, id)
				m.removedMeasures = append(m.removedMeasures, id)
			}
		}
		for id := range m.replaces {
			if _, registered := m.models[id]; !registered {
				delete(m.replaces, id)
			}
		}
		m.measMu.Unlock()
	}
	if wake {
		m.wakeLocked()
	}
	var report MeasurementReport
	metrics, publish := m.metrics.(measurementMetrics)
	if publish {
		report = m.measurementsLocked()
	}
	m.mu.Unlock()
	// Logging and metric publication never run under the admission lock.
	if publish {
		metrics.SetMeasurements(report)
	}
	for _, message := range overshoots {
		log.Print(message)
	}
}

// recordLocked folds one sample into a model's record. It reports whether the
// reservation ledger changed and, for an overshoot, the message to log once
// the lock is released. complete is false when some memory of the sample could
// not be attributed: the model's value may then be too low, so it may raise a
// high-water mark but is not taken as its idle footprint. Callers hold mu.
func (m *Manager) recordLocked(e *entry, was, is observation, usedMiB int, complete bool, now time.Time) (bool, string) {
	model := e.model
	model.ContainerID = was.id
	observed := m.measurementLocked(was.id)
	reservation := m.peakReservation(model)

	m.measMu.Lock()
	observed.LastMiB, observed.SampledAt = usedMiB, now
	if model.Name != "" {
		observed.Name = model.Name
	}
	if model.ImageID != "" {
		observed.ImageID = model.ImageID
	}
	observed.GPUUUID = model.GPUUUID
	message := ""
	if (was.live || is.live) && usedMiB > observed.PeakMiB {
		// A new high-water mark above the current reservation means the
		// reservation was too small. Raising the stored peak raises every
		// later measured reservation at once.
		if model.Resources.GPURequired && reservation > 0 && usedMiB > reservation {
			observed.Overshoots++
			message = fmt.Sprintf("[model-manager] measured GPU memory %d MiB exceeds the %d MiB reservation of model %s", usedMiB, reservation, model.Name)
		}
		observed.PeakMiB = usedMiB
		observed.dirty = true
	}
	if complete && was.idle && is.idle && was.lastUsed.Equal(is.lastUsed) && usedMiB > observed.ResidentMiB {
		observed.ResidentMiB = usedMiB
		observed.dirty = true
	}
	m.measMu.Unlock()

	// A prediction counts as observed only when a sample can have seen its
	// working memory: one taken at least MeasuredMinBusy after inference
	// began, or two during the same prediction. A sample at the instant the
	// model turns busy, or while it loads, shows little more than the idle
	// footprint and must not lower the reservation to it.
	if was.predicting && is.predicting && was.busySince.Equal(is.busySince) && usedMiB > 0 {
		e.busySamples++
		if was.at.Sub(was.busySince) >= m.config.MeasuredMinBusy || e.busySamples >= 2 {
			e.sampledActive = true
		}
	}
	if message != "" {
		m.metrics.ObserveEvent("reservation_overshoot", "detected")
	}
	// With measured reservations a held reservation follows the raised peak,
	// limited to the free budget so the ledger never exceeds the device.
	if m.config.ReservationSource != ReservationMeasured || !model.Resources.GPURequired || e.reserved == 0 {
		return false, message
	}
	if e.state != types.RuntimeReady && e.state != types.RuntimeBusy && e.state != types.RuntimeLoading {
		return false, message
	}
	want := m.peakReservation(model)
	if is.idle {
		want = m.residentReservation(model)
	}
	free := m.deviceCapacity(model.GPUUUID) - m.deviceUsedLocked(model.GPUUUID)
	if want <= e.reserved || free <= 0 {
		return false, message
	}
	e.reserved += min(want-e.reserved, free)
	return true, message
}

func (s *gpuSampler) persist(ctx context.Context, now time.Time) {
	if s.store == nil {
		return
	}
	m := s.manager
	m.measMu.Lock()
	gap := measurementSaveGap
	if s.storeFailed {
		gap = time.Minute
	}
	if now.Sub(s.lastSave) < gap {
		m.measMu.Unlock()
		return
	}
	var upsert []Measurement
	var saved []*measurement
	for _, observed := range m.measurements {
		if observed.dirty {
			observed.dirty = false
			upsert = append(upsert, observed.Measurement)
			saved = append(saved, observed)
		}
	}
	remove := m.removedMeasures
	m.removedMeasures = nil
	m.measMu.Unlock()
	if len(upsert) == 0 && len(remove) == 0 {
		return
	}
	s.lastSave = now
	saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := s.store.Save(saveCtx, upsert, remove)
	cancel()
	if err == nil {
		if s.storeFailed {
			log.Printf("[model-manager] GPU measurement persistence recovered")
		}
		s.storeFailed = false
		return
	}
	// Measurement continues in memory; the same records are retried later.
	if !s.storeFailed {
		log.Printf("[model-manager] GPU measurements are not being persisted: %v", err)
	}
	s.storeFailed = true
	m.measMu.Lock()
	for _, observed := range saved {
		observed.dirty = true
	}
	m.removedMeasures = append(m.removedMeasures, remove...)
	m.measMu.Unlock()
}
