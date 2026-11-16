package modelmanager

import (
	"context"
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

// GPUUsage is the last per-process sample of one device.
type GPUUsage struct {
	ManagedMiB, ForeignMiB int
	SampledAt              time.Time
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
	peak = max(m.scaleMeasured(observed.PeakMiB), observed.ResidentMiB)
	if observed.ResidentMiB > 0 {
		resident = m.scaleMeasured(observed.ResidentMiB)
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

func (m *Manager) kickMeasurement() {
	select {
	case m.measureKick <- struct{}{}:
	default:
	}
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
	id, gpu    string
	live       bool // loading, predicting, loaded or otherwise holding a lease
	idle, busy bool
	lastUsed   time.Time
}

func (m *Manager) observeLocked() map[string]observation {
	observed := make(map[string]observation, len(m.models))
	for id, e := range m.models {
		observed[id] = observation{
			id: id, gpu: e.model.GPUUUID, lastUsed: e.lastUsed,
			live: e.loaded || e.active || e.leased,
			idle: e.loaded && !e.active && !e.leased && e.state == types.RuntimeReady,
			busy: e.active && (e.state == types.RuntimeLoading || e.state == types.RuntimeBusy),
		}
	}
	return observed
}

// gpuSampler owns the state of the measurement loop. It is used by a single
// goroutine; everything shared with admission lives in the Manager.
type gpuSampler struct {
	manager     *Manager
	probe       GPUProbe
	store       MeasurementStore
	now         func() time.Time
	owners      map[string]map[string]string // GPU -> host PID -> container ID ("" is foreign)
	checked     map[string]time.Time         // foreign host PID -> last ownership lookup
	probeID     map[string]string            // GPU -> container that last answered
	attempted   map[string]time.Time
	lastSave    time.Time
	storeFailed bool
}

func newGPUSampler(m *Manager, probe GPUProbe, store MeasurementStore) *gpuSampler {
	return &gpuSampler{manager: m, probe: probe, store: store, now: time.Now, owners: map[string]map[string]string{}, checked: map[string]time.Time{}, probeID: map[string]string{}, attempted: map[string]time.Time{}}
}

// RunGPUMeasurement samples per-process GPU memory until the context ends. It
// only reads: it never starts a container and never changes a model's state.
func (m *Manager) RunGPUMeasurement(ctx context.Context, probe GPUProbe, store MeasurementStore) {
	if !m.config.Enabled || m.config.MeasureInterval <= 0 || probe == nil {
		return
	}
	sampler := newGPUSampler(m, probe, store)
	ticker := time.NewTicker(m.config.MeasureInterval)
	defer ticker.Stop()
	// A prediction start or end asks for an extra sample so that short
	// predictions are seen; the gap bounds how often that can happen.
	gap := min(m.config.MeasureInterval, 500*time.Millisecond)
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.measureKick:
			if time.Since(last) < gap {
				continue
			}
		}
		last = time.Now()
		sampler.sample(ctx)
	}
}

type gpuSample struct {
	processes []dockerSDK.GPUProcess
	owners    map[string]string
}

func (s *gpuSampler) sample(ctx context.Context) {
	m := s.manager
	m.mu.Lock()
	before := m.observeLocked()
	m.mu.Unlock()

	byGPU := map[string][]observation{}
	for _, item := range before {
		if item.gpu != "" {
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
		if !live && now.Sub(s.attempted[gpu]) < idleGPUSampleInterval {
			continue
		}
		s.attempted[gpu] = now
		processes, ok := s.read(ctx, gpu, models)
		if !ok {
			// No running container to ask, or the probe failed: keep the
			// previous observations and try again on the next cycle.
			m.metrics.ObserveEvent("gpu_sample", "unavailable")
			continue
		}
		m.metrics.ObserveEvent("gpu_sample", "success")
		samples[gpu] = gpuSample{processes: processes, owners: s.resolve(ctx, gpu, models, processes, now)}
	}
	s.apply(before, byGPU, samples, now)
	s.persist(ctx, now)
}

// read asks one running container pinned to the GPU, preferring the one that
// answered last and then the models known to be loaded.
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
		processes, err := s.probe.ReadGPUProcesses(ctx, id, gpu)
		if err == nil {
			s.probeID[gpu] = id
			return processes, true
		}
	}
	delete(s.probeID, gpu)
	return nil, false
}

// resolve maps the host PIDs of one sample to model containers. Docker is only
// asked when a PID is new, so a steady state costs no process listing.
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
		for _, item := range models {
			pids, err := s.probe.ContainerProcessIDs(ctx, item.id)
			if err != nil {
				continue // a stopped container owns no process
			}
			for _, pid := range pids {
				containers[pid] = item.id
			}
		}
		for _, process := range processes {
			if _, done := owners[process.PID]; done {
				continue
			}
			owners[process.PID] = containers[process.PID]
			if containers[process.PID] == "" {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	after := m.observeLocked()
	wake := false
	for gpu, sample := range samples {
		perModel := map[string]int{}
		usage := GPUUsage{SampledAt: now}
		for _, process := range sample.processes {
			owner := sample.owners[process.PID]
			if e, ok := m.models[owner]; owner != "" && ok && e.model.GPUUUID == gpu {
				perModel[owner] += process.UsedMemoryMiB
				usage.ManagedMiB += process.UsedMemoryMiB
			} else {
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
			if m.recordLocked(e, was, after[was.id], perModel[was.id], now) {
				wake = true
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
	if metrics, ok := m.metrics.(measurementMetrics); ok {
		metrics.SetMeasurements(m.measurementsLocked())
	}
}

// recordLocked folds one sample into a model's record and reports whether the
// reservation ledger changed. Callers hold mu.
func (m *Manager) recordLocked(e *entry, was, is observation, usedMiB int, now time.Time) bool {
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
	overshoot := false
	if (was.live || is.live) && usedMiB > observed.PeakMiB {
		// A new high-water mark above the current reservation means the
		// reservation was too small. Raising the stored peak raises every
		// later measured reservation at once.
		overshoot = model.Resources.GPURequired && reservation > 0 && usedMiB > reservation
		if overshoot {
			observed.Overshoots++
		}
		observed.PeakMiB = usedMiB
		observed.dirty = true
	}
	samePhase := was.lastUsed.Equal(is.lastUsed)
	if was.idle && is.idle && samePhase && usedMiB > observed.ResidentMiB {
		observed.ResidentMiB = usedMiB
		observed.dirty = true
	}
	m.measMu.Unlock()

	if was.busy && is.busy && samePhase && usedMiB > 0 {
		e.sampledActive = true
	}
	if overshoot {
		m.metrics.ObserveEvent("reservation_overshoot", "detected")
		log.Printf("[model-manager] measured GPU memory %d MiB exceeds the %d MiB reservation of model %s", usedMiB, reservation, model.Name)
	}
	// With measured reservations a held reservation follows the raised peak,
	// limited to the free budget so the ledger never exceeds the device.
	if m.config.ReservationSource != ReservationMeasured || !model.Resources.GPURequired || e.reserved == 0 {
		return false
	}
	if e.state != types.RuntimeReady && e.state != types.RuntimeBusy && e.state != types.RuntimeLoading {
		return false
	}
	want := m.peakReservation(model)
	if is.idle {
		want = m.residentReservation(model)
	}
	free := m.deviceCapacity(model.GPUUUID) - m.deviceUsedLocked(model.GPUUUID)
	if want <= e.reserved || free <= 0 {
		return false
	}
	e.reserved += min(want-e.reserved, free)
	return true
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
