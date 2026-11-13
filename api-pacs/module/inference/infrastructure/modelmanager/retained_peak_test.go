package modelmanager

import (
	"context"
	"math"
	"testing"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func conservativeManager(t *testing.T, b Backend) *Manager {
	t.Helper()
	env := map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "100", "GPU_MEMORY_SAFETY_MARGIN_MIB": "0"}
	config, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(config, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEnabledConfigurationRetainsPeakUnlessExplicitlyDisabled(t *testing.T) {
	env := map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "24576"}
	config, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil || !config.RetainPeakWhileLoaded {
		t.Fatalf("unsafe default: %+v %v", config, err)
	}
	env["MODEL_RETAIN_PEAK_WHILE_LOADED"] = "false"
	config, err = ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil || config.RetainPeakWhileLoaded {
		t.Fatalf("explicit opt-out ignored: %+v %v", config, err)
	}
	env["MODEL_RETAIN_PEAK_WHILE_LOADED"] = "perhaps"
	if _, err := ConfigFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("invalid retention flag accepted")
	}
}

func TestRetainedPeakPreventsOverbookingAfterSuccessfulInference(t *testing.T) {
	b := newFake()
	b.add(1, 10, 70)
	b.add(2, 10, 70)
	m := conservativeManager(t, b)
	if _, err := m.Predict(context.Background(), id(1), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.ReservedMiB != 70 || s.ResidentMiB != 70 || s.ActiveMiB != 0 || s.AvailableMiB != 30 {
		t.Fatalf("inference completion released unconfirmed CUDA capacity: %+v", s)
	}
	m.mu.Lock()
	m.models[id(1)].lastUsed = time.Now().Add(-time.Hour)
	m.mu.Unlock()
	b.predict = func(_ context.Context, model Model, _ api.PredictRequest) (api.PredictResponse, error) {
		b.mu.Lock()
		firstLoaded := b.loaded[id(1)]
		b.mu.Unlock()
		if firstLoaded {
			t.Error("second model admitted before first model unloaded")
		}
		return api.PredictResponse{Success: true}, nil
	}
	if _, err := m.Predict(context.Background(), id(2), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(b.unloads) != 1 || b.unloads[0] != id(1) {
		t.Fatalf("expected pressure eviction: %v", b.unloads)
	}
	if s := m.Snapshot(); s.ReservedMiB != 70 {
		t.Fatalf("incorrect retained ledger: %+v", s)
	}
}

func TestReconciliationRetainsPeakForAlreadyLoadedRuntime(t *testing.T) {
	b := newFake()
	b.add(1, 10, 70)
	b.loaded[id(1)] = true
	m := conservativeManager(t, b)
	if err := m.Reconcile(context.Background(), []string{id(1)}); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.ReservedMiB != 70 || s.ResidentMiB != 70 {
		t.Fatalf("restart undercounted cached resident: %+v", s)
	}
	b.add(2, 10, 70)
	b.loaded[id(2)] = true
	if err := m.Reconcile(context.Background(), []string{id(1), id(2)}); err == nil {
		t.Fatal("overcommitted loaded runtimes accepted at startup")
	}
}

func TestConservativePeakScalingRoundsUpAndPreservesImageContract(t *testing.T) {
	b := newFake()
	b.add(1, 10, 61)
	m := conservativeManager(t, b)
	m.config.GPUReservationPercent = 150
	if got := m.peakReservation(b.models[id(1)]); got != 92 {
		t.Fatalf("scaled peak must round up: %d", got)
	}
	if _, err := m.Predict(context.Background(), id(1), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	if s := m.Snapshot(); s.ReservedMiB != 92 {
		t.Fatalf("scaled retention missing: %+v", s)
	}
	if m.models[id(1)].model.Resources.PeakMemoryMiB != 61 {
		t.Fatal("immutable model resource declaration was modified")
	}
	// A warm prediction must still compare the original contract consistently.
	if _, err := m.Predict(context.Background(), id(1), api.PredictRequest{}); err != nil {
		t.Fatal(err)
	}
	b.add(2, 10, 70)
	if _, err := m.Predict(context.Background(), id(2), api.PredictRequest{}); err == nil {
		t.Fatal("scaled peak exceeding capacity was admitted")
	}
	if len(b.loads) != 1 {
		t.Fatalf("overcapacity request reached GPU: %v", b.loads)
	}
}

func TestConservativePeakScalingConfigurationValidation(t *testing.T) {
	env := map[string]string{"MODEL_MANAGER_ENABLED": "true", "GPU_MEMORY_BUDGET_MIB": "24576", "MODEL_GPU_MEMORY_RESERVATION_PERCENT": "150"}
	c, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil || c.GPUReservationPercent != 150 {
		t.Fatalf("scaled config: %+v %v", c, err)
	}
	for _, value := range []string{"0", "99", "1001", "invalid"} {
		env["MODEL_GPU_MEMORY_RESERVATION_PERCENT"] = value
		if _, err := ConfigFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("unsafe percentage accepted: %s", value)
		}
	}
}

func TestOversizedScaledReconciliationAndLedgersFailClosed(t *testing.T) {
	b := newFake()
	b.add(1, 10, 20)
	b.add(2, 10, math.MaxInt)
	b.loaded[id(1)] = true
	b.loaded[id(2)] = true
	m := conservativeManager(t, b)
	m.config.GPUReservationPercent = 150
	if got := m.peakReservation(b.models[id(2)]); got != math.MaxInt {
		t.Fatalf("huge scaled peak wrapped: %d", got)
	}
	if err := m.Reconcile(context.Background(), []string{id(1), id(2)}); err == nil {
		t.Fatal("overflowing startup inventory accepted")
	}
	m.models[id(1)] = &entry{model: b.models[id(1)], reserved: math.MaxInt, loaded: true}
	m.models[id(2)] = &entry{model: b.models[id(2)], reserved: math.MaxInt, loaded: true}
	if s := m.Snapshot(); s.ReservedMiB != math.MaxInt || s.AvailableMiB != 0 || s.ResidentMiB < 0 || s.ActiveMiB < 0 {
		t.Fatalf("overflowing ledger exposed capacity: %+v", s)
	}
	m.config.GPUBudgetsMiB = map[string]int{gpuA: 100}
	m.models[id(1)].model.GPUUUID = gpuA
	m.models[id(2)].model.GPUUUID = gpuA
	if used := m.deviceUsedLocked(gpuA); used != math.MaxInt {
		t.Fatalf("device memory wrapped: %d", used)
	}
	if gpu := m.Snapshot().GPUs[gpuA]; gpu.ReservedMiB != math.MaxInt || gpu.AvailableMiB != 0 || gpu.ResidentMiB < 0 || gpu.ActiveMiB < 0 {
		t.Fatalf("device ledger exposed capacity: %+v", gpu)
	}
}

func TestAdministrativePlacementUsesScaledPhysicalBudget(t *testing.T) {
	b := newFake()
	m := conservativeManager(t, b)
	m.config.GPUBudgetsMiB = map[string]int{gpuA: 100}
	m.config.GPUReservationPercent = 150
	if err := m.ValidateGPUPlacement(gpuA, resources(10, 60)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		uuid     string
		resource api.ModelResources
	}{{gpuB, resources(10, 60)}, {gpuA, resources(10, 70)}, {gpuA, resources(0, 0)}} {
		if err := m.ValidateGPUPlacement(tc.uuid, tc.resource); err == nil {
			t.Fatal("unsupported placement accepted")
		}
	}
}

func TestStoredGPUPreferenceSupportsScalarBudgetWithoutEnablingMoves(t *testing.T) {
	m := conservativeManager(t, newFake())
	if err := m.ValidateGPUPreference(gpuA, resources(10, 60)); err != nil {
		t.Fatalf("stored UUID rejected under scalar budget: %v", err)
	}
	if err := m.ValidateGPUPlacement(gpuA, resources(10, 60)); err == nil {
		t.Fatal("explicit moves require per-device budgets")
	}
	if err := m.ValidateGPUPreference(gpuA, resources(10, 101)); err == nil {
		t.Fatal("stored preference cannot exceed scalar capacity")
	}
	if err := m.ValidateGPUPreference("0", resources(10, 60)); err == nil {
		t.Fatal("ordinal preference accepted")
	}
}
