package modelmanager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
)

// loadedOn reconciles n loaded 3000 MiB models, all on GPU A unless listed.
func loadedOn(t *testing.T, source string, change func(*Config), gpus ...string) (*Manager, *fakeBackend, *fakeProbe) {
	t.Helper()
	b := newFake()
	var ids []string
	for i, gpu := range gpus {
		b.add(i+1, 2000, 3000)
		assign(b, i+1, gpu)
		b.loaded[id(i+1)] = true
		ids = append(ids, id(i+1))
	}
	m := measuredManager(t, b, source, change)
	if err := m.Reconcile(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	return m, b, newProbe()
}

func TestHungNvidiaSMIIsBoundedNotRetriedElsewhereAndBackedOff(t *testing.T) {
	m, _, probe := loadedOn(t, ReservationDeclared, func(c *Config) { c.MeasureInterval = 2 * time.Second }, gpuA, gpuA, gpuB)
	probe.pids[id(3)] = []string{"30"}
	probe.use(gpuB, process("30", 2100))
	probe.hungRead[id(1)], probe.hungRead[id(2)] = true, true
	sampler := newGPUSampler(m, probe, nil)
	sampler.readTimeout = 30 * time.Millisecond
	now := time.Now()
	sampler.now = func() time.Time { return now }

	started := time.Now()
	sampler.sample(context.Background(), nil)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a hung exec held the sampler for %s", elapsed)
	}
	attemptsOnA := func() int {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		count := 0
		for _, container := range probe.attempts {
			if container != id(3) {
				count++
			}
		}
		return count
	}
	// One exec on the stuck GPU, not one per container; the healthy GPU is unaffected.
	if attemptsOnA() != 1 || m.Measurements().Models[id(3)].LastMiB != 2100 || !m.Measurements().GPUs[gpuA].SampledAt.IsZero() {
		t.Fatalf("attempts=%v report=%+v", probe.attempts, m.Measurements().GPUs)
	}
	// Exponential backoff: 2 s, 4 s, 8 s between attempts while it keeps failing.
	for step, wait := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
		now = now.Add(wait - time.Millisecond)
		sampler.sample(context.Background(), nil)
		if attemptsOnA() != step+1 {
			t.Fatalf("step %d: retried before the backoff elapsed: %v", step, probe.attempts)
		}
		now = now.Add(time.Millisecond)
		sampler.sample(context.Background(), nil)
		if attemptsOnA() != step+2 {
			t.Fatalf("step %d: not retried after the backoff: %v", step, probe.attempts)
		}
	}
	// The delay is capped, and a success resets it.
	for i := 0; i < 20; i++ {
		now = now.Add(maxProbeBackoff)
		sampler.sample(context.Background(), nil)
	}
	if delay := sampler.retryAt[gpuA].Sub(now); delay != maxProbeBackoff {
		t.Fatalf("backoff is not capped: %s", delay)
	}
	probe.set(func() { probe.hungRead[id(1)], probe.hungRead[id(2)] = false, false })
	now = now.Add(maxProbeBackoff)
	sampler.sample(context.Background(), nil)
	if sampler.failures[gpuA] != 0 || m.Measurements().GPUs[gpuA].SampledAt.IsZero() {
		t.Fatalf("the GPU did not recover: %+v", sampler.failures)
	}
}

func TestDriverErrorDoesNotFallThroughButAStoppedContainerDoes(t *testing.T) {
	m, _, probe := loadedOn(t, ReservationDeclared, nil, gpuA, gpuA, gpuA)
	sampler := newGPUSampler(m, probe, nil)
	probe.readErr[gpuA] = errors.New("GPU telemetry command failed")
	sampler.sample(context.Background(), nil)
	if len(probe.attempts) != 1 {
		t.Fatalf("a driver error was repeated in other containers: %v", probe.attempts)
	}
	probe.set(func() { delete(probe.readErr, gpuA); probe.stopped[id(1)], probe.stopped[id(2)] = true, true })
	sampler.now = func() time.Time { return time.Now().Add(time.Minute) }
	sampler.sample(context.Background(), nil)
	if len(probe.reads) != 1 || probe.reads[0] != id(3) {
		t.Fatalf("a stopped container must give way to a running one: attempts=%v reads=%v", probe.attempts, probe.reads)
	}
}

func TestHungOrFailingProcessListingNeverStopsTheSamplerOrInventsForeignMemory(t *testing.T) {
	m, _, probe := loadedOn(t, ReservationDeclared, nil, gpuA, gpuA)
	probe.pids[id(1)], probe.pids[id(2)] = []string{"10"}, []string{"20"}
	probe.use(gpuA, process("10", 2100), process("20", 2200))
	sampler := newGPUSampler(m, probe, nil)
	sampler.ownerTimeout = 30 * time.Millisecond

	// docker top hangs for a running container: bounded, and its process is
	// neither foreign nor attributed.
	probe.hungTop[id(2)] = true
	started := time.Now()
	sampler.sample(context.Background(), nil)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a hung process listing held the sampler for %s", elapsed)
	}
	report := m.Measurements()
	if usage := report.GPUs[gpuA]; usage.ForeignMiB != 0 || usage.UnattributedMiB != 2200 || usage.ManagedMiB != 2100 {
		t.Fatalf("an unlisted container's memory was called foreign: %+v", usage)
	}
	if report.Models[id(1)].LastMiB != 2100 || report.Models[id(1)].MeasuredResidentMiB != 0 {
		t.Fatalf("an incomplete sample must not set an idle footprint: %+v", report.Models[id(1)])
	}
	// It is looked up again on the very next sample, not after the foreign recheck delay.
	probe.set(func() { probe.hungTop[id(2)] = false })
	sampler.sample(context.Background(), nil)
	report = m.Measurements()
	if usage := report.GPUs[gpuA]; usage.ForeignMiB != 0 || usage.UnattributedMiB != 0 || usage.ManagedMiB != 4300 || report.Models[id(2)].LastMiB != 2200 {
		t.Fatalf("ownership was not resolved once Docker answered: %+v %+v", usage, report.Models[id(2)])
	}

	// A transient error while a new PID is looked up keeps known owners.
	probe.set(func() { probe.topErr[id(1)] = errors.New("docker daemon is busy") })
	probe.use(gpuA, process("10", 2100), process("20", 2200), process("77", 500))
	sampler.sample(context.Background(), nil)
	report = m.Measurements()
	if usage := report.GPUs[gpuA]; usage.ManagedMiB != 4300 || usage.ForeignMiB != 0 || usage.UnattributedMiB != 500 {
		t.Fatalf("known ownership was lost on a transient error: %+v", usage)
	}
	// Once every container answers, a PID nobody owns is foreign.
	probe.set(func() { delete(probe.topErr, id(1)) })
	sampler.sample(context.Background(), nil)
	if usage := m.Measurements().GPUs[gpuA]; usage.ForeignMiB != 500 || usage.UnattributedMiB != 0 {
		t.Fatalf("foreign memory was not recognised: %+v", usage)
	}
	// A stopped container is certain to own nothing: no doubt, no delay.
	probe.set(func() { probe.stopped[id(2)] = true })
	probe.use(gpuA, process("10", 2100), process("88", 300))
	sampler.sample(context.Background(), nil)
	if usage := m.Measurements().GPUs[gpuA]; usage.ForeignMiB != 300 || usage.UnattributedMiB != 0 {
		t.Fatalf("a stopped container made ownership uncertain: %+v", usage)
	}
}

func TestOwnershipIsForgottenWhenAGPUStopsAnsweringSoAReusedPIDIsNotMisattributed(t *testing.T) {
	m, _, probe := loadedOn(t, ReservationDeclared, nil, gpuA, gpuA)
	probe.pids[id(1)], probe.pids[id(2)] = []string{"10"}, []string{"20"}
	probe.use(gpuA, process("10", 2100), process("20", 2200))
	sampler := newGPUSampler(m, probe, nil)
	now := time.Now()
	sampler.now = func() time.Time { return now }
	sampler.sample(context.Background(), nil)

	// During an outage model 1 restarts and the kernel gives its old PID to
	// a process of model 2.
	probe.set(func() { probe.readErr[gpuA] = dockerSDK.ErrGPUProbeTimeout })
	sampler.sample(context.Background(), nil)
	probe.set(func() {
		delete(probe.readErr, gpuA)
		probe.pids[id(1)], probe.pids[id(2)] = []string{"11"}, []string{"20", "10"}
	})
	probe.use(gpuA, process("10", 900), process("20", 2200), process("11", 2100))
	now = now.Add(time.Minute)
	sampler.sample(context.Background(), nil)
	report := m.Measurements()
	if report.Models[id(1)].LastMiB != 2100 || report.Models[id(2)].LastMiB != 3100 {
		t.Fatalf("a reused PID kept its old owner: %+v", report.Models)
	}
}

func TestPredictionsKickOnlyTheirOwnGPUAndAtMostOncePerGap(t *testing.T) {
	m, b, probe := loadedOn(t, ReservationDeclared, func(c *Config) {
		c.MeasureInterval = time.Hour // no tick during the test
		c.MeasuredMinBusy = time.Hour // only the end of a prediction kicks
	}, gpuA, gpuB)
	_ = b
	probe.pids[id(1)], probe.pids[id(2)] = []string{"10"}, []string{"20"}
	probe.use(gpuA, process("10", 2100))
	probe.use(gpuB, process("20", 2200))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.RunGPUMeasurement(ctx, probe, nil)

	readsOf := func(container string) int {
		probe.mu.Lock()
		defer probe.mu.Unlock()
		count := 0
		for _, read := range probe.reads {
			if read == container {
				count++
			}
		}
		return count
	}
	receive(t, request(m, 1))
	until(t, func() bool { return readsOf(id(1)) == 1 })
	// A burst of predictions inside the gap adds no exec, and GPU B is never read.
	for i := 0; i < 20; i++ {
		receive(t, request(m, 1))
	}
	time.Sleep(50 * time.Millisecond)
	if readsOf(id(1)) != 1 || readsOf(id(2)) != 0 {
		t.Fatalf("kicks were not limited to one GPU and one gap: %v", probe.reads)
	}
	receive(t, request(m, 2))
	until(t, func() bool { return readsOf(id(2)) == 1 })
	if readsOf(id(1)) != 1 {
		t.Fatalf("a prediction on GPU B sampled GPU A: %v", probe.reads)
	}
	if m.Measurements().Models[id(1)].LastMiB != 2100 || m.Measurements().Models[id(2)].LastMiB != 2200 {
		t.Fatal("kicked samples were not recorded")
	}
}

func TestPredictionIsObservedOnlyBySamplesThatCanSeeItsWorkingMemory(t *testing.T) {
	m, b, probe := loadedOn(t, ReservationMeasured, func(c *Config) { c.MeasuredMinBusy = 150 * time.Millisecond }, gpuA)
	started, release := make(chan struct{}), make(chan struct{})
	b.predict = func(context.Context, Model, api.PredictRequest) (api.PredictResponse, error) {
		started <- struct{}{}
		<-release
		return api.PredictResponse{Success: true}, nil
	}
	probe.pids[id(1)] = []string{"10"}
	probe.use(gpuA, process("10", 2050))
	sampler := newGPUSampler(m, probe, nil)
	samples := func() int { return m.Measurements().Models[id(1)].Samples }
	run := func(during func()) {
		t.Helper()
		done := request(m, 1)
		<-started
		during()
		release <- struct{}{}
		receive(t, done)
	}

	// One sample at the instant the model turns busy shows the idle footprint.
	run(func() { sampler.sample(context.Background(), nil) })
	if samples() != 0 {
		t.Fatal("a sample at the start of a prediction counted as an observation")
	}
	// A sample far enough into the prediction observes it.
	run(func() {
		time.Sleep(200 * time.Millisecond)
		sampler.sample(context.Background(), nil)
	})
	if samples() != 1 {
		t.Fatal("a sample well into the prediction was not counted:", samples())
	}
	// So do two samples of the same prediction, however early the first was.
	run(func() {
		sampler.sample(context.Background(), nil)
		sampler.sample(context.Background(), nil)
	})
	if samples() != 2 {
		t.Fatal("a prediction sampled twice was not counted:", samples())
	}
	// Two predictions sampled once each at their start are still not observed.
	run(func() { sampler.sample(context.Background(), nil) })
	run(func() { sampler.sample(context.Background(), nil) })
	if samples() != 2 {
		t.Fatal("early samples of different predictions were added up:", samples())
	}
	// The production default needs ten observed predictions, not five.
	if got := (&Manager{}).config.MeasuredMinSamples; got != 0 || defaultMeasuredMinSamples != 10 {
		t.Fatal("default minimum samples:", defaultMeasuredMinSamples)
	}
}

func TestLoadedModelNeverHoldsMoreThanItsMeasuredPeakReservation(t *testing.T) {
	b := newFake()
	b.add(1, 2000, 3000)
	assign(b, 1, gpuA)
	m := measuredManager(t, b, ReservationMeasured, func(c *Config) { c.RetainPeakWhileLoaded = false })
	// Enough observed predictions, but the model was never sampled idle.
	seed(t, m, Measurement{ContainerID: id(1), PeakMiB: 500, Samples: 5})
	receive(t, request(m, 1))
	if ledger := m.Snapshot(); ledger.Models[id(1)].ReservedMiB != 881 || ledger.GPUs[gpuA].ReservedMiB != 881 {
		t.Fatalf("an idle model holds more than it was admitted with: %+v", ledger.Models[id(1)])
	}
}

func TestRestartOverCapacityUnloadsIdleModelsInsteadOfFailingClosed(t *testing.T) {
	setup := func(source string) (*Manager, *fakeBackend, []string) {
		b := newFake()
		var ids []string
		for n := 1; n <= 3; n++ {
			b.add(n, 2000, 3000) // 4500 MiB declared at 150%
			assign(b, n, gpuA)
			b.loaded[id(n)] = true
			ids = append(ids, id(n))
		}
		used := time.Now().Add(-time.Hour)
		b.inspect = func(_ context.Context, container string) (Model, api.ModelRuntime, error) {
			// Model 2 was used longest ago, then 1, then 3.
			order := map[string]time.Duration{id(2): 0, id(1): time.Minute, id(3): 2 * time.Minute}
			at := used.Add(order[container])
			b.mu.Lock()
			defer b.mu.Unlock()
			if !b.loaded[container] {
				return b.models[container], api.ModelRuntime{State: api.RuntimeUnloaded}, nil
			}
			return b.models[container], api.ModelRuntime{State: api.RuntimeReady, Loaded: true, LastUsedAt: &at}, nil
		}
		return measuredManager(t, b, source, nil), b, ids
	}

	// (a) Three models were co-loaded under measured reservations (2756 MiB
	// each). The gateway restarts in declared mode, or without its stored
	// measurements: 13500 MiB of declarations on a 10000 MiB device.
	m, b, ids := setup(ReservationDeclared)
	if err := m.Reconcile(context.Background(), ids); err != nil {
		t.Fatal("reconciliation failed closed:", err)
	}
	ledger := m.Snapshot()
	if !ledger.Ready || ledger.GPUs[gpuA].ReservedMiB != 9000 || len(b.unloads) != 1 || b.unloads[0] != id(2) {
		t.Fatalf("expected the least recently used model to be unloaded: %+v unloads=%v", ledger.GPUs[gpuA], b.unloads)
	}
	if ledger.Models[id(2)].State != api.RuntimeUnloaded || ledger.Models[id(2)].Loaded || ledger.Models[id(1)].State != api.RuntimeReady {
		t.Fatalf("wrong states: %+v", ledger.Models)
	}
	receive(t, request(m, 1)) // admission works

	// (b) Measured mode, but an overshoot stored a peak the clamped ledger
	// never held: after a restart the reservations add up to more than fits.
	m, b, ids = setup(ReservationMeasured)
	seed(t, m, Measurement{ContainerID: id(1), PeakMiB: 3300, Samples: 5}, Measurement{ContainerID: id(2), PeakMiB: 3300, Samples: 5}, Measurement{ContainerID: id(3), PeakMiB: 2000, Samples: 5})
	if err := m.Reconcile(context.Background(), ids); err != nil { // 4381 + 4381 + 2756
		t.Fatal("reconciliation failed closed:", err)
	}
	if ledger = m.Snapshot(); !ledger.Ready || ledger.GPUs[gpuA].ReservedMiB != 4381+2756 || len(b.unloads) != 1 || b.unloads[0] != id(2) {
		t.Fatalf("measured restart: %+v unloads=%v", ledger.GPUs[gpuA], b.unloads)
	}

	// A model that cannot be unloaded is quarantined and the next one goes.
	m, b, ids = setup(ReservationDeclared)
	b.unload = func(_ context.Context, model Model) error {
		if model.ContainerID == id(2) {
			return errors.New("process did not exit")
		}
		return nil
	}
	if err := m.Reconcile(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	ledger = m.Snapshot()
	if !ledger.Ready || ledger.Models[id(2)].State != api.RuntimeError || ledger.Models[id(2)].ReservedMiB != 4500 || ledger.Models[id(1)].State != api.RuntimeUnloaded || ledger.GPUs[gpuA].ReservedMiB != 9000 {
		t.Fatalf("failed unload was not quarantined: %+v", ledger.Models)
	}

	// Nothing can be unloaded: the device stays over budget, quarantined, and
	// admits nothing, but the manager is ready and the other GPU keeps working.
	m, b, ids = setup(ReservationDeclared)
	b.add(4, 2000, 3000)
	assign(b, 4, gpuB)
	b.unload = func(context.Context, Model) error { return errors.New("process did not exit") }
	if err := m.Reconcile(context.Background(), append(ids, id(4))); err != nil {
		t.Fatal(err)
	}
	ledger = m.Snapshot()
	if !ledger.Ready || ledger.GPUs[gpuA].ReservedMiB != 13500 || ledger.GPUs[gpuA].AvailableMiB != 0 {
		t.Fatalf("over-budget device: %+v", ledger.GPUs[gpuA])
	}
	for n := 1; n <= 3; n++ {
		if ledger.Models[id(n)].State != api.RuntimeError {
			t.Fatalf("model %d was not quarantined: %+v", n, ledger.Models[id(n)])
		}
	}
	b.unload = nil
	receive(t, request(m, 4))
}

func TestSamplerRunsConcurrentlyWithPredictionsAndReconciliation(t *testing.T) {
	m, b, probe := loadedOn(t, ReservationMeasured, func(c *Config) { c.MeasureInterval = time.Millisecond }, gpuA, gpuA, gpuB)
	_ = b
	for n := 1; n <= 3; n++ {
		probe.pids[id(n)] = []string{string(rune('0' + n))}
	}
	probe.use(gpuA, process("1", 2100), process("2", 2200), process("9", 100))
	probe.use(gpuB, process("3", 2300))
	ctx, cancel := context.WithCancel(context.Background())
	var wait sync.WaitGroup
	wait.Add(1)
	go func() { defer wait.Done(); m.RunGPUMeasurement(ctx, probe, &memoryStore{}) }()
	for worker := 0; worker < 3; worker++ {
		wait.Add(1)
		go func(n int) {
			defer wait.Done()
			for ctx.Err() == nil {
				_, _ = m.Predict(ctx, id(n), api.PredictRequest{})
				_ = m.Measurements()
				_ = m.Snapshot()
			}
		}(worker + 1)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for ctx.Err() == nil {
			// Refused while admission is active; succeeds in the gaps.
			_ = m.Reconcile(ctx, []string{id(1), id(2), id(3)})
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	wait.Wait()
	if report := m.Measurements(); report.GPUs[gpuA].SampledAt.IsZero() || report.GPUs[gpuB].SampledAt.IsZero() {
		t.Fatalf("the sampler did not run: %+v", report.GPUs)
	}
}

func TestSampleAgeGaugeGrowsWhenAGPUIsNoLongerSampled(t *testing.T) {
	const fresh, stale, never = "GPU-aaaaaaaa-0000-0000-0000-000000000001", "GPU-aaaaaaaa-0000-0000-0000-000000000002", "GPU-aaaaaaaa-0000-0000-0000-000000000003"
	expvarMetrics{}.SetMeasurements(MeasurementReport{GPUs: map[string]GPUUsage{
		fresh: {SampledAt: time.Now(), ForeignMiB: 640, UnattributedMiB: 7},
		stale: {SampledAt: time.Now().Add(-90 * time.Second)},
		never: {},
	}})
	ages := gpuSampleAges()
	if ages["gpu="+fresh] < 0 || ages["gpu="+fresh] > 5 || ages["gpu="+stale] < 89 || ages["gpu="+never] != -1 {
		t.Fatalf("wrong ages: %v", ages)
	}
	if expvarMapValue(modelManagerGPUProcessMemoryMiB, "gpu="+fresh+",kind=foreign") != 640 || expvarMapValue(modelManagerGPUProcessMemoryMiB, "gpu="+fresh+",kind=unattributed") != 7 {
		t.Fatal("process memory gauges were not published")
	}
	if modelManagerGPUProcessMemoryMiB.Get("gpu="+never+",kind=foreign") != nil {
		t.Fatal("a GPU that was never sampled reports process memory")
	}
}
