package modelmanager

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
)

type fakeProbe struct {
	mu        sync.Mutex
	processes map[string][]dockerSDK.GPUProcess // by GPU
	pids      map[string][]string               // by container
	stopped   map[string]bool
	reads     []string
	tops      int
	onRead    func()
}

func newProbe() *fakeProbe {
	return &fakeProbe{processes: map[string][]dockerSDK.GPUProcess{}, pids: map[string][]string{}, stopped: map[string]bool{}}
}
func (p *fakeProbe) ReadGPUProcesses(_ context.Context, container, gpu string) ([]dockerSDK.GPUProcess, error) {
	p.mu.Lock()
	if p.stopped[container] {
		p.mu.Unlock()
		return nil, errors.New("GPU telemetry probe is unavailable")
	}
	p.reads = append(p.reads, container)
	processes := append([]dockerSDK.GPUProcess(nil), p.processes[gpu]...)
	hook := p.onRead
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	return processes, nil
}
func (p *fakeProbe) ContainerProcessIDs(_ context.Context, container string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tops++
	if p.stopped[container] {
		return nil, errors.New("container is not running")
	}
	return p.pids[container], nil
}
func (p *fakeProbe) use(gpu string, processes ...dockerSDK.GPUProcess) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processes[gpu] = processes
}

type fakeMetrics struct {
	mu     sync.Mutex
	events map[string]int
}

func (*fakeMetrics) SetSnapshot(Snapshot)                           {}
func (*fakeMetrics) ObserveReconciliation(string, time.Duration)    {}
func (*fakeMetrics) ObserveQueueWait(string, time.Duration)         {}
func (*fakeMetrics) ObserveOperation(string, string, time.Duration) {}
func (f *fakeMetrics) ObserveEvent(event, outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events == nil {
		f.events = map[string]int{}
	}
	f.events[event+"/"+outcome]++
}

type memoryStore struct {
	mu      sync.Mutex
	rows    map[string]Measurement
	err     error
	saves   int
	removed []string
}

func (s *memoryStore) Load(context.Context) ([]Measurement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []Measurement
	for _, row := range s.rows {
		rows = append(rows, row)
	}
	return rows, s.err
}
func (s *memoryStore) Save(_ context.Context, upsert []Measurement, remove []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.rows == nil {
		s.rows = map[string]Measurement{}
	}
	s.saves++
	for _, row := range upsert {
		s.rows[row.ContainerID] = row
	}
	for _, id := range remove {
		delete(s.rows, id)
		s.removed = append(s.removed, id)
	}
	return nil
}

// measuredManager has one 10000 MiB device budget per GPU and scales
// declarations by 150%, like production.
func measuredManager(t *testing.T, b Backend, source string, change func(*Config)) *Manager {
	t.Helper()
	config := Config{Enabled: true, RetainPeakWhileLoaded: true, GPUReservationPercent: 150, GPUBudgetsMiB: map[string]int{gpuA: 10000, gpuB: 10000}, QueueTimeout: time.Second, OperationTimeout: time.Second, ReservationSource: source}
	if change != nil {
		change(&config)
	}
	m, err := New(config, b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func process(pid string, mib int) dockerSDK.GPUProcess {
	return dockerSDK.GPUProcess{PID: pid, UsedMemoryMiB: mib}
}

func seed(t *testing.T, m *Manager, rows ...Measurement) {
	t.Helper()
	store := &memoryStore{rows: map[string]Measurement{}}
	for _, row := range rows {
		store.rows[row.ContainerID] = row
	}
	if err := m.LoadMeasurements(context.Background(), store); err != nil {
		t.Fatal(err)
	}
}

func TestSamplerAttributesHostPIDsToModelsAndCountsForeignMemory(t *testing.T) {
	b := newFake()
	for n, gpu := range map[int]string{1: gpuA, 2: gpuA, 3: gpuB} {
		b.add(n, 2000, 3000)
		assign(b, n, gpu)
		b.loaded[id(n)] = true
	}
	m := measuredManager(t, b, ReservationDeclared, nil)
	if err := m.Reconcile(context.Background(), []string{id(1), id(2), id(3)}); err != nil {
		t.Fatal(err)
	}
	probe := newProbe()
	probe.pids[id(1)] = []string{"100", "101", "102"} // supervisor, worker, subprocess
	probe.pids[id(2)] = []string{"200"}
	probe.pids[id(3)] = []string{"300"}
	probe.use(gpuA, process("101", 1898), process("102", 300), process("200", 2374), process("999", 700))
	probe.use(gpuB, process("300", 3300))
	sampler := newGPUSampler(m, probe, nil)
	sampler.sample(context.Background())

	report := m.Measurements()
	if got := report.Models[id(1)]; got.LastMiB != 2198 || got.MeasuredPeakMiB != 2198 || got.MeasuredResidentMiB != 2198 || !got.Sampled {
		t.Fatalf("two processes of one container must be summed: %+v", got)
	}
	if report.Models[id(2)].LastMiB != 2374 || report.Models[id(3)].LastMiB != 3300 {
		t.Fatalf("wrong attribution: %+v", report.Models)
	}
	if usage := report.GPUs[gpuA]; usage.ForeignMiB != 700 || usage.ManagedMiB != 4572 {
		t.Fatalf("foreign memory must be separate: %+v", usage)
	}
	if usage := report.GPUs[gpuB]; usage.ForeignMiB != 0 || usage.ManagedMiB != 3300 {
		t.Fatalf("a container only reports its own GPU: %+v", usage)
	}
	// Known PIDs need no further process listing; one exec per GPU per cycle.
	tops, reads := probe.tops, len(probe.reads)
	if reads != 2 {
		t.Fatalf("expected one probe per GPU, got %v", probe.reads)
	}
	sampler.sample(context.Background())
	if probe.tops != tops || len(probe.reads) != reads+2 {
		t.Fatalf("steady state listed processes again: tops %d -> %d reads=%v", tops, probe.tops, probe.reads)
	}
	// A PID that moves to another container (PID reuse after a restart) is
	// attributed again once it has been absent.
	probe.use(gpuA, process("200", 2374))
	sampler.sample(context.Background())
	probe.pids[id(1)], probe.pids[id(2)] = []string{"200"}, []string{"201"}
	probe.use(gpuA, process("201", 50))
	sampler.sample(context.Background())
	probe.use(gpuA, process("200", 1000), process("201", 50))
	sampler.sample(context.Background())
	if report = m.Measurements(); report.Models[id(1)].LastMiB != 1000 || report.Models[id(2)].LastMiB != 50 {
		t.Fatalf("stale PID ownership: %+v", report.Models)
	}
}

func TestSamplerSkipsAGPUWithoutARunningContainerAndPrefersLoadedModels(t *testing.T) {
	b := newFake()
	for n, gpu := range map[int]string{1: gpuA, 2: gpuA, 3: gpuB} {
		b.add(n, 2000, 3000)
		assign(b, n, gpu)
	}
	b.loaded[id(2)] = true
	m := measuredManager(t, b, ReservationDeclared, nil)
	if err := m.Reconcile(context.Background(), []string{id(1), id(2), id(3)}); err != nil {
		t.Fatal(err)
	}
	probe := newProbe()
	probe.stopped[id(3)] = true
	probe.pids[id(2)] = []string{"20"}
	probe.use(gpuA, process("20", 2100))
	sampler := newGPUSampler(m, probe, nil)
	sampler.sample(context.Background())
	if len(probe.reads) != 1 || probe.reads[0] != id(2) {
		t.Fatalf("the loaded model must be the probe: %v", probe.reads)
	}
	report := m.Measurements()
	if _, sampled := report.GPUs[gpuB]; sampled || report.Models[id(3)].Sampled {
		t.Fatalf("a GPU with no running container must be skipped: %+v", report)
	}
	// The stopped probe falls back to another container on the same GPU.
	probe.stopped[id(2)] = true
	sampler.sample(context.Background())
	if last := probe.reads[len(probe.reads)-1]; last != id(1) {
		t.Fatalf("expected a fallback probe, got %v", probe.reads)
	}
	// Nothing is loaded on GPU B, so it is retried slowly, not on every cycle.
	now := time.Now()
	sampler.now = func() time.Time { return now }
	probe.stopped[id(3)] = false
	sampler.sample(context.Background())
	if m.Measurements().Models[id(3)].Sampled {
		t.Fatal("an idle GPU was sampled again at once")
	}
	sampler.now = func() time.Time { return now.Add(idleGPUSampleInterval + time.Second) }
	sampler.sample(context.Background())
	if !m.Measurements().Models[id(3)].Sampled {
		t.Fatal("an idle GPU must still be sampled eventually")
	}
}

func TestMeasurementTracksLoadPredictIdleAndUnload(t *testing.T) {
	b := newFake()
	b.add(1, 2000, 3000)
	assign(b, 1, gpuA)
	loading, started, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finishLoad := make(chan struct{})
	b.load = func(context.Context, Model) error { close(loading); <-finishLoad; return nil }
	b.predict = func(context.Context, Model, api.PredictRequest) (api.PredictResponse, error) {
		started <- struct{}{}
		<-release
		return api.PredictResponse{Success: true}, nil
	}
	m := measuredManager(t, b, ReservationDeclared, nil)
	probe := newProbe()
	probe.pids[id(1)] = []string{"10"}
	sampler := newGPUSampler(m, probe, nil)
	offset := time.Duration(0)
	sample := func(mib int) ModelMeasurement {
		t.Helper()
		// Step past the slow sampling of a GPU with nothing loaded.
		offset += time.Minute
		at := time.Now().Add(offset)
		sampler.now = func() time.Time { return at }
		if mib > 0 {
			probe.use(gpuA, process("10", mib))
		} else {
			probe.use(gpuA)
		}
		sampler.sample(context.Background())
		return m.Measurements().Models[id(1)]
	}

	done := request(m, 1)
	<-loading
	if got := sample(1500); got.MeasuredPeakMiB != 1500 || got.MeasuredResidentMiB != 0 || got.Samples != 0 {
		t.Fatalf("loading counts towards the peak only: %+v", got)
	}
	close(finishLoad)
	<-started
	if got := sample(2900); got.MeasuredPeakMiB != 2900 || got.MeasuredResidentMiB != 0 {
		t.Fatalf("an active prediction is never the idle footprint: %+v", got)
	}
	release <- struct{}{}
	receive(t, done)
	if got := m.Measurements().Models[id(1)]; got.Samples != 1 {
		t.Fatalf("a sampled prediction must be counted once it completes: %+v", got)
	}
	if got := sample(2100); got.MeasuredPeakMiB != 2900 || got.MeasuredResidentMiB != 2100 || got.LastMiB != 2100 {
		t.Fatalf("idle memory is the resident footprint: %+v", got)
	}

	// A prediction that no sample saw is not counted as observed.
	done = request(m, 1)
	<-started
	release <- struct{}{}
	receive(t, done)
	if got := m.Measurements().Models[id(1)]; got.Samples != 1 {
		t.Fatalf("an unobserved prediction was counted: %+v", got)
	}

	// A prediction that ends between the Docker read and the bookkeeping must
	// not be recorded as idle memory.
	done = request(m, 1)
	<-started
	probe.onRead = func() { release <- struct{}{}; receive(t, done) }
	if got := sample(2800); got.MeasuredResidentMiB != 2100 || got.MeasuredPeakMiB != 2900 {
		t.Fatalf("a straddling sample changed the resident footprint: %+v", got)
	}
	probe.onRead = nil

	if err := m.BlockAndDrain(context.Background(), id(1)); err != nil {
		t.Fatal(err)
	}
	if got := sample(0); got.LastMiB != 0 || got.MeasuredPeakMiB != 2900 || got.MeasuredResidentMiB != 2100 || got.Samples != 1 {
		t.Fatalf("unloading keeps the learned values: %+v", got)
	}
	// Memory left by an unloaded model is reported but never learned.
	if got := sample(4000); got.LastMiB != 4000 || got.MeasuredPeakMiB != 2900 {
		t.Fatalf("an unloaded model raised its peak: %+v", got)
	}
}

func TestReservationComputationInBothSources(t *testing.T) {
	// Declared 3467 MiB at 150% reserves 5201 MiB, as CathEF-CLIP does today.
	model := Model{ContainerID: id(1), GPUUUID: gpuA, Resources: resources(2000, 3467)}
	for _, test := range []struct {
		name               string
		source             string
		observed           Measurement
		retain             bool
		percent            int
		wantPeak, wantHeld int
	}{
		{"declared ignores measurements", ReservationDeclared, Measurement{PeakMiB: 2374, ResidentMiB: 2000, Samples: 50}, true, 0, 5201, 5201},
		{"measured without observations", ReservationMeasured, Measurement{}, true, 0, 5201, 5201},
		{"below the minimum samples", ReservationMeasured, Measurement{PeakMiB: 2374, ResidentMiB: 2000, Samples: 4}, true, 0, 5201, 5201},
		{"measured peak with safety and headroom", ReservationMeasured, Measurement{PeakMiB: 2374, ResidentMiB: 2000, Samples: 5}, true, 0, 3224, 3224},
		{"safety percent rounds up", ReservationMeasured, Measurement{PeakMiB: 2001, ResidentMiB: 100, Samples: 5}, true, 0, 2758, 2758},
		{"custom safety percent", ReservationMeasured, Measurement{PeakMiB: 2000, ResidentMiB: 100, Samples: 9}, true, 200, 4256, 4256},
		{"capped at the declared reservation", ReservationMeasured, Measurement{PeakMiB: 5000, ResidentMiB: 4000, Samples: 5}, true, 0, 5201, 5201},
		{"never below the measured resident", ReservationMeasured, Measurement{PeakMiB: 1000, ResidentMiB: 2500, Samples: 5}, true, 0, 2500, 2500},
		{"declared resident without retained peak", ReservationDeclared, Measurement{PeakMiB: 2374, ResidentMiB: 1000, Samples: 5}, false, 0, 5201, 2000},
		{"measured resident without retained peak", ReservationMeasured, Measurement{PeakMiB: 2374, ResidentMiB: 1000, Samples: 5}, false, 0, 3224, 1506},
		{"measured resident capped at its declaration", ReservationMeasured, Measurement{PeakMiB: 2374, ResidentMiB: 1900, Samples: 5}, false, 0, 3224, 2000},
		{"resident needs the minimum samples too", ReservationMeasured, Measurement{PeakMiB: 2374, ResidentMiB: 1000, Samples: 1}, false, 0, 5201, 2000},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := measuredManager(t, newFake(), test.source, func(c *Config) {
				c.RetainPeakWhileLoaded = test.retain
				c.MeasuredSafetyPercent = test.percent
			})
			test.observed.ContainerID = id(1)
			seed(t, m, test.observed)
			if peak, held := m.peakReservation(model), m.residentReservation(model); peak != test.wantPeak || held != test.wantHeld {
				t.Fatalf("peak=%d resident=%d, want %d and %d", peak, held, test.wantPeak, test.wantHeld)
			}
			described := m.ModelReservation(id(1), gpuA, model.Resources)
			wantSource := ReservationDeclared
			if test.source == ReservationMeasured && test.observed.Samples >= 5 {
				wantSource = ReservationMeasured
			}
			if described.ReservationMiB != test.wantPeak || described.DeclaredReservationMiB != 5201 || described.DeclaredPeakMiB != 3467 || described.ReservationSource != wantSource {
				t.Fatalf("wrong description: %+v", described)
			}
		})
	}
	// CPU models and other containers are never affected.
	m := measuredManager(t, newFake(), ReservationMeasured, nil)
	seed(t, m, Measurement{ContainerID: id(1), PeakMiB: 900, Samples: 9})
	if got := m.peakReservation(Model{ContainerID: id(1), Resources: resources(0, 0)}); got != 0 {
		t.Fatal("a CPU model received a GPU reservation:", got)
	}
	if got := m.peakReservation(Model{ContainerID: id(2), Resources: resources(2000, 3467)}); got != 5201 {
		t.Fatal("another container inherited a measurement:", got)
	}
}

func TestMeasuredReservationsAdmitModelsTheDeclarationsCouldNot(t *testing.T) {
	admitted := func(source string) (int, int) {
		b := newFake()
		for n := 1; n <= 3; n++ {
			b.add(n, 2000, 3000) // 4500 MiB each when declared at 150%
			assign(b, n, gpuA)
			model := b.models[id(n)]
			model.Resources.IdleTimeoutSeconds = 3600
			b.models[id(n)] = model
		}
		m := measuredManager(t, b, source, func(c *Config) { c.QueueTimeout = 300 * time.Millisecond })
		for n := 1; n <= 3; n++ {
			seed(t, m, Measurement{ContainerID: id(n), PeakMiB: 2000, ResidentMiB: 1800, Samples: 5})
		}
		loaded := 0
		for n := 1; n <= 3; n++ {
			if err := <-request(m, n); err == nil {
				loaded++
			}
		}
		return loaded, m.Snapshot().GPUs[gpuA].ReservedMiB
	}
	if loaded, reserved := admitted(ReservationDeclared); loaded != 2 || reserved != 9000 {
		t.Fatalf("declared reservations: loaded=%d reserved=%d", loaded, reserved)
	}
	if loaded, reserved := admitted(ReservationMeasured); loaded != 3 || reserved != 3*2756 {
		t.Fatalf("measured reservations: loaded=%d reserved=%d", loaded, reserved)
	}
}

func TestOvershootRaisesTheStoredPeakAndTheHeldReservation(t *testing.T) {
	run := func(source string, budget int) (*Manager, *fakeMetrics) {
		b := newFake()
		b.add(1, 2000, 3000)
		assign(b, 1, gpuA)
		b.add(2, 2000, 3000)
		assign(b, 2, gpuA)
		m := measuredManager(t, b, source, func(c *Config) { c.GPUBudgetsMiB = map[string]int{gpuA: budget, gpuB: budget} })
		metrics := &fakeMetrics{}
		m.metrics = metrics
		seed(t, m, Measurement{ContainerID: id(1), PeakMiB: 2000, ResidentMiB: 1800, Samples: 5}, Measurement{ContainerID: id(2), PeakMiB: 2000, ResidentMiB: 1800, Samples: 5})
		receive(t, request(m, 1))
		receive(t, request(m, 2))
		probe := newProbe()
		probe.pids[id(1)], probe.pids[id(2)] = []string{"10"}, []string{"20"}
		probe.use(gpuA, process("10", 3000), process("20", 1900))
		sampler := newGPUSampler(m, probe, nil)
		sampler.sample(context.Background())
		sampler.sample(context.Background())
		return m, metrics
	}

	m, metrics := run(ReservationMeasured, 10000)
	report, ledger := m.Measurements(), m.Snapshot()
	if got := report.Models[id(1)]; got.MeasuredPeakMiB != 3000 || got.Overshoots != 1 || got.ReservationMiB != 4006 {
		t.Fatalf("the stored peak must follow the sample at once: %+v", got)
	}
	if ledger.Models[id(1)].ReservedMiB != 4006 || ledger.Models[id(2)].ReservedMiB != 2756 {
		t.Fatalf("the held reservation must follow: %+v", ledger.Models)
	}
	if report.Models[id(2)].Overshoots != 0 || metrics.events["reservation_overshoot/detected"] != 1 {
		t.Fatalf("overshoots must be counted once per new high-water mark: %+v %v", report.Models[id(2)], metrics.events)
	}

	// The ledger never exceeds the device: the raise is limited to what is free.
	m, _ = run(ReservationMeasured, 6000)
	if ledger = m.Snapshot(); ledger.Models[id(1)].ReservedMiB != 3244 || ledger.GPUs[gpuA].ReservedMiB != 6000 || ledger.GPUs[gpuA].AvailableMiB != 0 {
		t.Fatalf("overshoot overbooked the device: %+v", ledger)
	}

	// Shadow mode reports reality above the declaration but changes nothing.
	b := newFake()
	b.add(1, 1000, 1000) // reserves 1500 MiB
	assign(b, 1, gpuA)
	m = measuredManager(t, b, ReservationDeclared, nil)
	receive(t, request(m, 1))
	probe := newProbe()
	probe.pids[id(1)] = []string{"10"}
	probe.use(gpuA, process("10", 1898))
	newGPUSampler(m, probe, nil).sample(context.Background())
	if got := m.Measurements().Models[id(1)]; got.Overshoots != 1 || got.ReservationMiB != 1500 || m.Snapshot().Models[id(1)].ReservedMiB != 1500 {
		t.Fatalf("shadow mode must only report: %+v", got)
	}
}

func TestMeasurementsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gpu-measurements.json")
	b := newFake()
	for n := 1; n <= 2; n++ {
		b.add(n, 2000, 3000)
		assign(b, n, gpuA)
		b.loaded[id(n)] = true
	}
	first := measuredManager(t, b, ReservationMeasured, func(c *Config) { c.MeasuredMinSamples = 1 })
	if err := first.Reconcile(context.Background(), []string{id(1), id(2)}); err != nil {
		t.Fatal(err)
	}
	if reserved := first.Snapshot().GPUs[gpuA].ReservedMiB; reserved != 9000 {
		t.Fatal("nothing measured yet, so both models hold their declaration:", reserved)
	}
	probe := newProbe()
	probe.pids[id(1)], probe.pids[id(2)] = []string{"10"}, []string{"20"}
	started, release := make(chan struct{}), make(chan struct{})
	b.predict = func(context.Context, Model, api.PredictRequest) (api.PredictResponse, error) {
		started <- struct{}{}
		<-release
		return api.PredictResponse{Success: true}, nil
	}
	store := &FileMeasurementStore{Path: path}
	sampler := newGPUSampler(first, probe, store)
	done := request(first, 1)
	<-started
	probe.use(gpuA, process("10", 2400), process("20", 1700))
	sampler.sample(context.Background())
	release <- struct{}{}
	receive(t, done)
	probe.use(gpuA, process("10", 2100), process("20", 1700))
	sampler.now = func() time.Time { return time.Now().Add(time.Minute) }
	sampler.sample(context.Background())

	// A new process starts with the stored observations and reconciles the
	// already loaded models with measured reservations.
	second := measuredManager(t, b, ReservationMeasured, func(c *Config) { c.MeasuredMinSamples = 1 })
	if err := second.LoadMeasurements(context.Background(), &FileMeasurementStore{Path: path}); err != nil {
		t.Fatal(err)
	}
	if err := second.Reconcile(context.Background(), []string{id(1), id(2)}); err != nil {
		t.Fatal(err)
	}
	got := second.Measurements().Models[id(1)]
	if got.MeasuredPeakMiB != 2400 || got.MeasuredResidentMiB != 2100 || got.Samples != 1 || got.ReservationMiB != 3256 || got.ReservationSource != ReservationMeasured {
		t.Fatalf("observations were lost by the restart: %+v", got)
	}
	if other := second.Measurements().Models[id(2)]; other.MeasuredPeakMiB != 1700 || other.Samples != 0 || other.ReservationMiB != 4500 {
		t.Fatalf("a model without observed predictions keeps its declaration: %+v", other)
	}
	if ledger := second.Snapshot(); ledger.Models[id(1)].ReservedMiB != 3256 || ledger.GPUs[gpuA].ReservedMiB != 3256+4500 {
		t.Fatalf("reconciliation ignored the stored measurement: %+v", ledger)
	}

	// A container that left the inventory is forgotten, also on disk.
	if err := second.Reconcile(context.Background(), []string{id(1)}); err != nil {
		t.Fatal(err)
	}
	resumed := newGPUSampler(second, probe, &FileMeasurementStore{Path: path})
	if _, err := resumed.store.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumed.sample(context.Background())
	rows, err := (&FileMeasurementStore{Path: path}).Load(context.Background())
	if err != nil || len(rows) != 1 || rows[0].ContainerID != id(1) {
		t.Fatalf("stale container was kept: %+v %v", rows, err)
	}
	// A missing file is an empty state, not an error.
	if rows, err = (&FileMeasurementStore{Path: filepath.Join(t.TempDir(), "absent.json")}).Load(context.Background()); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestMeasurementPersistenceFailureNeverStopsSampling(t *testing.T) {
	b := newFake()
	b.add(1, 2000, 3000)
	assign(b, 1, gpuA)
	b.loaded[id(1)] = true
	m := measuredManager(t, b, ReservationDeclared, nil)
	if err := m.Reconcile(context.Background(), []string{id(1)}); err != nil {
		t.Fatal(err)
	}
	probe := newProbe()
	probe.pids[id(1)] = []string{"10"}
	probe.use(gpuA, process("10", 2100))
	store := &memoryStore{err: errors.New(`relation "model_gpu_measurements" does not exist`)}
	sampler := newGPUSampler(m, probe, store)
	now := time.Now()
	sampler.now = func() time.Time { return now }
	sampler.sample(context.Background())
	if got := m.Measurements().Models[id(1)]; got.MeasuredResidentMiB != 2100 {
		t.Fatalf("sampling must continue in memory: %+v", got)
	}
	// Once storage works again the unsaved record is written.
	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()
	sampler.now = func() time.Time { return now.Add(2 * time.Minute) }
	sampler.sample(context.Background())
	if store.rows[id(1)].ResidentMiB != 2100 {
		t.Fatalf("the record was not retried: %+v", store.rows)
	}
}

func TestMeasurementsFollowAReplacementContainerOfTheSameImageOnly(t *testing.T) {
	for _, test := range []struct {
		name, newImage string
		wantSamples    int
	}{{"same image on another GPU", "sha256:one", 6}, {"upgraded image", "sha256:two", 0}} {
		t.Run(test.name, func(t *testing.T) {
			b := newFake()
			b.add(1, 2000, 3000)
			assign(b, 1, gpuA)
			b.add(2, 2000, 3000)
			assign(b, 2, gpuB)
			old, candidate := b.models[id(1)], b.models[id(2)]
			old.ImageID, candidate.ImageID = "sha256:one", test.newImage
			b.models[id(1)], b.models[id(2)] = old, candidate
			m := measuredManager(t, b, ReservationMeasured, nil)
			seed(t, m, Measurement{ContainerID: id(1), ImageID: "sha256:one", PeakMiB: 2000, ResidentMiB: 1800, Samples: 6, Overshoots: 2})
			if err := m.Reconcile(context.Background(), []string{id(1)}); err != nil {
				t.Fatal(err)
			}
			if err := m.BlockAndDrain(context.Background(), id(1)); err != nil {
				t.Fatal(err)
			}
			if err := m.ReplaceDrained(id(1), id(2)); err != nil {
				t.Fatal(err)
			}
			if err := m.PrepareBlocked(context.Background(), id(2)); err != nil {
				t.Fatal(err)
			}
			got := m.Measurements().Models[id(2)]
			wantReservation := 4500
			if test.wantSamples > 0 {
				wantReservation = 2756
			}
			if got.Samples != test.wantSamples || got.ReservationMiB != wantReservation || got.Overshoots != 0 || m.Snapshot().Models[id(2)].ReservedMiB != wantReservation {
				t.Fatalf("wrong inheritance: %+v", got)
			}
		})
	}
}

func TestSamplerNeverHoldsTheManagerLockAcrossDockerCalls(t *testing.T) {
	b := newFake()
	b.add(1, 2000, 3000)
	assign(b, 1, gpuA)
	b.loaded[id(1)] = true
	m := measuredManager(t, b, ReservationDeclared, nil)
	if err := m.Reconcile(context.Background(), []string{id(1)}); err != nil {
		t.Fatal(err)
	}
	probe := newProbe()
	probe.pids[id(1)] = []string{"10"}
	probe.use(gpuA, process("10", 2100))
	// Admission of the model completes while the probe is still blocked.
	probe.onRead = func() { receive(t, request(m, 1)) }
	newGPUSampler(m, probe, nil).sample(context.Background())
	if len(probe.reads) != 1 {
		t.Fatal(probe.reads)
	}
}

func TestMeasurementLoopHonoursTheDisabledInterval(t *testing.T) {
	m := measuredManager(t, newFake(), ReservationDeclared, nil) // MeasureInterval 0
	done := make(chan struct{})
	go func() { m.RunGPUMeasurement(context.Background(), newProbe(), nil); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a disabled sampler kept running")
	}
	// An enabled loop samples on a prediction even before its first tick.
	b := newFake()
	b.add(1, 2000, 3000)
	assign(b, 1, gpuA)
	m = measuredManager(t, b, ReservationDeclared, func(c *Config) { c.MeasureInterval = time.Hour })
	probe := newProbe()
	probe.pids[id(1)] = []string{"10"}
	probe.use(gpuA, process("10", 2100))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.RunGPUMeasurement(ctx, probe, nil)
	receive(t, request(m, 1))
	until(t, func() bool { return m.Measurements().Models[id(1)].LastMiB == 2100 })
}

func TestMeasurementSettingsAreValidated(t *testing.T) {
	base := map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGETS_MIB": `{"` + gpuA + `":24564,"` + gpuB + `":24576}`, "GPU_MEMORY_SAFETY_MARGIN_MIB": "4096"}
	load := func(extra map[string]string) (Config, error) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return ConfigFromEnv(func(k string) string { return env[k] })
	}
	c, err := load(nil)
	if err != nil || c.MeasureInterval != 2*time.Second || c.ReservationSource != ReservationDeclared || c.MeasuredMinSamples != 5 || c.MeasuredSafetyPercent != 125 ||
		c.PlacementMode != PlacementPinned || c.PlacementInterval != 300*time.Second || len(c.AllowedGPUUUIDs) != 0 || c.MeasureStatePath != "" {
		t.Fatalf("wrong defaults: %+v %v", c, err)
	}
	c, err = load(map[string]string{"MODEL_GPU_MEASURE_INTERVAL_SECONDS": "0", "MODEL_GPU_MEASURE_STATE_PATH": " /data/gpu.json ", "MODEL_RESERVATION_SOURCE": "declared", "MODEL_MEASURED_MIN_SAMPLES": "9", "MODEL_MEASURED_SAFETY_PERCENT": "150",
		"MODEL_GPU_PLACEMENT": "auto", "MODEL_GPU_PLACEMENT_INTERVAL_SECONDS": "60", "MODEL_GPU_ALLOWED_UUIDS": gpuA + " , " + gpuB, "MODEL_GPU_PLACEMENT_TENANT_ID": "tenant-a"})
	if err != nil || c.MeasureInterval != 0 || c.MeasureStatePath != "/data/gpu.json" || c.MeasuredMinSamples != 9 || c.MeasuredSafetyPercent != 150 || c.PlacementMode != PlacementAuto ||
		c.PlacementInterval != time.Minute || len(c.AllowedGPUUUIDs) != 2 || c.PlacementTenantID != "tenant-a" || c.PlacementActor != "system:gpu-auto-placement" {
		t.Fatalf("settings were not read: %+v %v", c, err)
	}
	for name, extra := range map[string]map[string]string{
		"interval":             {"MODEL_GPU_MEASURE_INTERVAL_SECONDS": "-1"},
		"interval text":        {"MODEL_GPU_MEASURE_INTERVAL_SECONDS": "2s"},
		"source":               {"MODEL_RESERVATION_SOURCE": "actual"},
		"measured needs data":  {"MODEL_RESERVATION_SOURCE": "measured", "MODEL_GPU_MEASURE_INTERVAL_SECONDS": "0"},
		"samples":              {"MODEL_MEASURED_MIN_SAMPLES": "0"},
		"safety below 100":     {"MODEL_MEASURED_SAFETY_PERCENT": "99"},
		"safety text":          {"MODEL_MEASURED_SAFETY_PERCENT": "x"},
		"placement":            {"MODEL_GPU_PLACEMENT": "automatic"},
		"placement interval":   {"MODEL_GPU_PLACEMENT_INTERVAL_SECONDS": "5"},
		"allowed malformed":    {"MODEL_GPU_ALLOWED_UUIDS": "gpu0"},
		"allowed unconfigured": {"MODEL_GPU_ALLOWED_UUIDS": "GPU-11111111-1111-1111-1111-111111111111"},
		"allowed duplicate":    {"MODEL_GPU_ALLOWED_UUIDS": gpuA + "," + gpuA},
		"allowed empty":        {"MODEL_GPU_ALLOWED_UUIDS": " , "},
		"auto needs a tenant":  {"MODEL_GPU_PLACEMENT": "auto"},
	} {
		if _, err := load(extra); err == nil {
			t.Errorf("%s: invalid setting accepted", name)
		}
	}
	// Planning needs one ledger per device.
	single := map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "81920", "MODEL_GPU_PLACEMENT": "propose"}
	if _, err := ConfigFromEnv(func(k string) string { return single[k] }); err == nil {
		t.Error("placement without per-GPU budgets accepted")
	}
}
