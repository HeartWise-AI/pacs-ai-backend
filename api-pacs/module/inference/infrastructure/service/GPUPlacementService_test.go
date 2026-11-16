package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

// placementBackend is a model lifecycle fake whose predictions can be held open.
type placementBackend struct {
	mu         sync.Mutex
	models     map[string]modelmanager.Model
	loaded     map[string]bool
	hold       chan struct{}
	started    chan string
	failUnload bool
}

func (b *placementBackend) runtime(id string) dockerAPI.ModelRuntime {
	if b.loaded[id] {
		return dockerAPI.ModelRuntime{State: dockerAPI.RuntimeReady, Loaded: true}
	}
	return dockerAPI.ModelRuntime{State: dockerAPI.RuntimeUnloaded}
}
func (b *placementBackend) Inspect(_ context.Context, id string) (modelmanager.Model, dockerAPI.ModelRuntime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.models[id], b.runtime(id), nil
}
func (b *placementBackend) Prepare(_ context.Context, id string) (modelmanager.Model, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.models[id], nil
}
func (b *placementBackend) Runtime(_ context.Context, m modelmanager.Model) (dockerAPI.ModelRuntime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runtime(m.ContainerID), nil
}
func (b *placementBackend) Load(_ context.Context, m modelmanager.Model) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loaded[m.ContainerID] = true
	return nil
}
func (b *placementBackend) Unload(_ context.Context, m modelmanager.Model) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failUnload {
		return errors.New("unload was not confirmed")
	}
	b.loaded[m.ContainerID] = false
	return nil
}
func (b *placementBackend) Predict(ctx context.Context, m modelmanager.Model, _ dockerAPI.PredictRequest) (dockerAPI.PredictResponse, error) {
	b.mu.Lock()
	hold, started := b.hold, b.started
	b.mu.Unlock()
	if hold != nil {
		started <- m.ContainerID
		select {
		case <-hold:
		case <-ctx.Done():
			return dockerAPI.PredictResponse{}, ctx.Err()
		}
	}
	return dockerAPI.PredictResponse{Success: true}, nil
}

type placementAPI struct {
	resources map[string]dockerAPI.ModelResources
}

func (a *placementAPI) GetModelInfo(_ context.Context, name string) (dockerAPI.GetModelInfoResponse, error) {
	resources, ok := a.resources[name]
	if !ok {
		return dockerAPI.GetModelInfoResponse{}, errors.New("unreachable model")
	}
	response := dockerAPI.GetModelInfoResponse{}
	response.Data.Resources = resources
	return response, nil
}

type placementProbe struct {
	processes map[string][]dockerSDK.GPUProcess
	pids      map[string][]string
}

func (p *placementProbe) ReadGPUProcesses(_ context.Context, _, gpu string) ([]dockerSDK.GPUProcess, error) {
	return p.processes[gpu], nil
}
func (p *placementProbe) ContainerProcessIDs(_ context.Context, id string) ([]string, error) {
	return p.pids[id], nil
}

type placementStore struct{ rows []modelmanager.Measurement }

func (s *placementStore) Load(context.Context) ([]modelmanager.Measurement, error) {
	return s.rows, nil
}
func (*placementStore) Save(context.Context, []modelmanager.Measurement, []string) error { return nil }

type placementFixture struct {
	service  *GPUAdministrationService
	manager  *modelmanager.Manager
	backend  *placementBackend
	models   *adminTestModels
	docker   *adminTestDocker
	api      *placementAPI
	deployer *adminTestDeployer
	clock    time.Time
}

func placementID(n int) string { return fmt.Sprintf("%064x", n) }

// newPlacementFixture registers four 300 MiB models on GPU A and one on GPU B.
// Each device schedules 900 MiB, so one model on A cannot be loaded with the
// others; "a1" has no catalog deployment and therefore cannot be moved.
func newPlacementFixture(t *testing.T, change func(*modelmanager.Config)) *placementFixture {
	t.Helper()
	config := modelmanager.Config{
		Enabled: true, RequireReconciliation: true, RetainPeakWhileLoaded: true, GPUReservationPercent: 100,
		GPUBudgetsMiB: map[string]int{adminGPUA: 1000, adminGPUB: 1000}, SafetyMarginMiB: 100, QueueTimeout: 5 * time.Second, OperationTimeout: 5 * time.Second,
		MinResidency: 30 * time.Second, PlacementMode: modelmanager.PlacementAuto, PlacementInterval: 5 * time.Minute, PlacementTenantID: "tenant-a",
	}
	if change != nil {
		change(&config)
	}
	fixture := &placementFixture{
		backend: &placementBackend{models: map[string]modelmanager.Model{}, loaded: map[string]bool{}},
		models:  &adminTestModels{}, docker: &adminTestDocker{infos: map[string]dockerTypes.GetContainerInfoResult{}},
		api: &placementAPI{resources: map[string]dockerAPI.ModelResources{}}, deployer: &adminTestDeployer{}, clock: time.Now(),
	}
	var ids []string
	for n, spec := range []struct{ name, gpu string }{{"a1", adminGPUA}, {"a2", adminGPUA}, {"a3", adminGPUA}, {"a4", adminGPUA}, {"b1", adminGPUB}} {
		id := placementID(n + 1)
		ids = append(ids, id)
		resources := dockerAPI.ModelResources{GPURequired: true, ResidentMemoryMiB: 200, PeakMemoryMiB: 300, MaxConcurrentInferences: 1, IdleTimeoutSeconds: 3600}
		fixture.backend.models[id] = modelmanager.Model{ContainerID: id, Name: spec.name, GPUUUID: spec.gpu, ImageID: "sha256:" + spec.name, Resources: resources}
		fixture.api.resources[spec.name] = resources
		fixture.docker.infos[id] = dockerTypes.GetContainerInfoResult{ID: id, Name: "/" + spec.name, GPUUUID: spec.gpu, Lifecycle: dockerTypes.LifecycleRunning}
		registered := entity.InferenceModel{ID: "model-" + spec.name, TenantID: "tenant-a", ContainerID: id, Deployment: &entity.InferenceModelDeployment{CatalogVersionID: "version-" + spec.name}}
		if spec.name == "a1" {
			registered.Deployment = nil
		}
		fixture.models.models = append(fixture.models.models, registered)
	}
	manager, err := modelmanager.New(config, fixture.backend)
	require.NoError(t, err)
	require.NoError(t, manager.Reconcile(context.Background(), ids))
	fixture.manager = manager
	fixture.service = &GPUAdministrationService{Manager: manager, Models: fixture.models, Docker: fixture.docker, API: fixture.api, Deployer: fixture.deployer}
	fixture.service.Clock = func() time.Time { return fixture.clock }
	return fixture
}

func (f *placementFixture) predict(t *testing.T, n int) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := f.manager.Predict(context.Background(), placementID(n), dockerAPI.PredictRequest{})
		done <- err
	}()
	return done
}

func TestPlacementPlanProposesWithoutSideEffects(t *testing.T) {
	fixture := newPlacementFixture(t, func(c *modelmanager.Config) { c.PlacementMode = modelmanager.PlacementPropose })
	plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Equal(t, "propose", plan.Mode)
	require.Equal(t, "declared", plan.ReservationSource)
	require.Equal(t, modelmanager.PlacementExhaustive, plan.Strategy)
	require.Equal(t, 5, plan.ModelsTotal)
	require.Equal(t, 4, plan.ModelsFitBefore)
	require.Equal(t, 5, plan.ModelsFitAfter)
	require.True(t, plan.Improves)
	require.Equal(t, adminGPUA, plan.Current["model-a2"])
	require.Equal(t, adminGPUB, plan.Proposed["model-a2"])
	require.Equal(t, adminGPUA, plan.Proposed["model-a1"], "a model without a catalog deployment stays")
	require.Equal(t, []GPUPlacementMoveView{{ModelID: "model-a2", ContainerID: placementID(2), FromGPUUUID: adminGPUA, ToGPUUUID: adminGPUB, ReservationMiB: 300}}, plan.Moves)
	require.Equal(t, []GPUPlacementDeviceView{
		{UUID: adminGPUA, Allowed: true, CapacityMiB: 900, ReservedBeforeMiB: 1200, ReservedAfterMiB: 900, ModelsBefore: 4, ModelsAfter: 3, ModelsFitBefore: 3, ModelsFitAfter: 3},
		{UUID: adminGPUB, Allowed: true, CapacityMiB: 900, ReservedBeforeMiB: 300, ReservedAfterMiB: 600, ModelsBefore: 1, ModelsAfter: 2, ModelsFitBefore: 1, ModelsFitAfter: 2},
	}, plan.Devices)
	require.Len(t, plan.Models, 5)
	require.False(t, plan.Models[0].Movable)
	require.Equal(t, "An immutable catalog deployment is required", plan.Models[0].Reason)
	require.False(t, plan.Auto.Enabled)
	require.Contains(t, plan.Auto.BlockedReason, "MODEL_GPU_PLACEMENT=propose")
	require.Empty(t, fixture.deployer.requests)
	// The propose mode never applies anything, even when asked to cycle.
	require.Empty(t, fixture.service.autoPlacementCycle(context.Background()))
	require.Empty(t, fixture.deployer.requests)
	require.Zero(t, fixture.manager.Snapshot().ReservedMiB)

	_, err = fixture.service.PlacementPlan(context.Background(), "")
	require.EqualError(t, err, apiError.MissingRecord)
}

func TestPlacementPlanIsRefusedWhilePinnedAndWithoutAManager(t *testing.T) {
	fixture := newPlacementFixture(t, func(c *modelmanager.Config) { c.PlacementMode = "" })
	_, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.ErrorIs(t, err, ErrGPUPlacementPinned)
	require.Empty(t, fixture.service.autoPlacementCycle(context.Background()))
	require.Empty(t, fixture.deployer.requests)
	_, err = (&GPUAdministrationService{}).PlacementPlan(context.Background(), "tenant-a")
	require.EqualError(t, err, apiError.InferenceUpgradeUnavailable)
	done := make(chan struct{})
	go func() { fixture.service.RunAutoPlacement(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the automatic loop must not run while pinned")
	}
}

func TestPlacementPlanSkipsUnplannableModelsAndCountsOtherWorkspaces(t *testing.T) {
	fixture := newPlacementFixture(t, func(c *modelmanager.Config) { c.PlacementMode = modelmanager.PlacementPropose })
	// b1 belongs to another workspace: its reservation is fixed load on GPU B.
	fixture.models.models[4].TenantID = "tenant-b"
	// a4's container is gone and a3 is a CPU model.
	delete(fixture.docker.infos, placementID(4))
	fixture.api.resources["a3"] = dockerAPI.ModelResources{MaxConcurrentInferences: 1, IdleTimeoutSeconds: 60}
	cpu := fixture.backend.models[placementID(3)]
	cpu.Resources = fixture.api.resources["a3"]
	fixture.backend.models[placementID(3)] = cpu
	require.NoError(t, fixture.manager.Reconcile(context.Background(), []string{placementID(1), placementID(2), placementID(3), placementID(4), placementID(5)}))
	plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Equal(t, 2, plan.ModelsTotal)
	require.NotContains(t, plan.Current, "model-b1")
	require.Equal(t, 300, plan.Devices[1].OtherReservedMiB)
	require.Equal(t, 600, plan.Devices[1].CapacityMiB)
	require.Empty(t, plan.Moves)
	require.ElementsMatch(t, []GPUPlacementSkippedView{
		{ModelID: "model-a3", Reason: "This model does not require a GPU"},
		{ModelID: "model-a4", Reason: "Container is unavailable"},
	}, plan.Skipped)
}

func TestAutoPlacementAppliesOneMovePerCycleThroughTheMovePath(t *testing.T) {
	fixture := newPlacementFixture(t, nil)
	outcome := fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "moved model model-a2")
	require.Contains(t, outcome, "4 of 5 models fit now, 5 after the plan")
	require.Len(t, fixture.deployer.requests, 1)
	request := fixture.deployer.requests[0]
	require.Equal(t, "tenant-a", request.TenantID)
	require.Equal(t, "system:gpu-auto-placement", request.ActorUserID)
	require.Equal(t, "model-a2", request.ModelID)
	require.Equal(t, "version-a2", request.VersionID)
	require.Equal(t, adminGPUB, request.TargetGPUUUID)
	require.Equal(t, placementID(2), request.ExpectedContainerID)

	// The deployment is asynchronous: nothing else moves until it is done.
	outcome = fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "previous automatic move is still being deployed")
	require.Len(t, fixture.deployer.requests, 1)
	plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.True(t, plan.Auto.Enabled)
	require.Equal(t, "model-a2", plan.Auto.LastMoveModel)
	require.NotNil(t, plan.Auto.LastMoveAt)
	require.Contains(t, plan.Auto.LastOutcome, "previous automatic move")

	// Once the model is served by its new container on GPU B, everything fits.
	moved := fmt.Sprintf("%064x", 22)
	fixture.models.models[1].ContainerID = moved
	fixture.docker.infos[moved] = dockerTypes.GetContainerInfoResult{ID: moved, Name: "/a2", GPUUUID: adminGPUB, Lifecycle: dockerTypes.LifecycleRunning}
	fixture.backend.models[moved] = modelmanager.Model{ContainerID: moved, Name: "a2", GPUUUID: adminGPUB, Resources: fixture.api.resources["a2"]}
	require.NoError(t, fixture.manager.Reconcile(context.Background(), []string{placementID(1), moved, placementID(3), placementID(4), placementID(5)}))
	outcome = fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "cannot be improved")
	require.Len(t, fixture.deployer.requests, 1)
}

func TestAutoPlacementWaitsForAnEmptyQueueAndAnIdleModel(t *testing.T) {
	fixture := newPlacementFixture(t, nil)
	fixture.backend.hold, fixture.backend.started = make(chan struct{}), make(chan string, 4)

	// a2, the model the plan moves, is predicting.
	busy := fixture.predict(t, 2)
	require.Equal(t, placementID(2), <-fixture.backend.started)
	outcome := fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "no planned move is safe now")
	require.Contains(t, outcome, "serving or has waiting requests")
	plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Len(t, plan.Moves, 1)
	require.Equal(t, "The model is serving or has waiting requests", plan.Moves[0].BlockedReason)
	require.Empty(t, plan.Auto.BlockedReason, "the plan itself is applicable; the move is what is unsafe")

	// A request for another model is waiting behind a busy one: nothing moves.
	queued := fixture.predict(t, 2)
	require.Eventually(t, func() bool { return fixture.manager.Snapshot().QueueDepth == 1 }, 2*time.Second, time.Millisecond)
	outcome = fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "Requests are waiting for admission")
	require.Empty(t, fixture.deployer.requests)

	close(fixture.backend.hold)
	require.NoError(t, <-busy)
	require.NoError(t, <-queued)

	// Idle but used a moment ago: inside the minimum residency it stays.
	outcome = fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "used too recently")
	require.Empty(t, fixture.deployer.requests)

	// Past the minimum residency the loaded model is evictable and may move.
	fixture.clock = fixture.clock.Add(time.Minute)
	outcome = fixture.service.autoPlacementCycle(context.Background())
	require.Contains(t, outcome, "moved model model-a2")
	require.Len(t, fixture.deployer.requests, 1)
}

func TestAutoPlacementNeverForcesAnUnsafeOrUselessMove(t *testing.T) {
	t.Run("plan does not improve the fit", func(t *testing.T) {
		fixture := newPlacementFixture(t, nil)
		fixture.api.resources["a4"] = dockerAPI.ModelResources{GPURequired: true, ResidentMemoryMiB: 800, PeakMemoryMiB: 850, MaxConcurrentInferences: 1, IdleTimeoutSeconds: 60}
		fixture.api.resources["b1"] = fixture.api.resources["a4"]
		// 850 MiB fits nowhere next to anything else: four models fit whatever moves.
		outcome := fixture.service.autoPlacementCycle(context.Background())
		require.Contains(t, outcome, "skipped")
		require.Empty(t, fixture.deployer.requests)
	})
	t.Run("only immovable models", func(t *testing.T) {
		fixture := newPlacementFixture(t, nil)
		for i := range fixture.models.models {
			fixture.models.models[i].Deployment = nil
		}
		outcome := fixture.service.autoPlacementCycle(context.Background())
		require.Contains(t, outcome, "cannot be improved")
		require.Empty(t, fixture.deployer.requests)
	})
	t.Run("destination outside the allowed GPUs", func(t *testing.T) {
		fixture := newPlacementFixture(t, func(c *modelmanager.Config) { c.AllowedGPUUUIDs = []string{adminGPUA} })
		plan, err := fixture.service.PlacementPlan(context.Background(), "tenant-a")
		require.NoError(t, err)
		require.Empty(t, plan.Moves)
		require.False(t, plan.Devices[1].Allowed)
		require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "cannot be improved")
		require.Empty(t, fixture.deployer.requests)
	})
	t.Run("quarantined model", func(t *testing.T) {
		fixture := newPlacementFixture(t, nil)
		fixture.backend.hold, fixture.backend.started = make(chan struct{}), make(chan string, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := fixture.manager.Predict(ctx, placementID(2), dockerAPI.PredictRequest{})
			done <- err
		}()
		<-fixture.backend.started
		fixture.backend.mu.Lock()
		fixture.backend.failUnload = true
		fixture.backend.mu.Unlock()
		cancel()
		require.Error(t, <-done)
		fixture.clock = fixture.clock.Add(time.Hour)
		outcome := fixture.service.autoPlacementCycle(context.Background())
		require.Contains(t, outcome, "not safe to move automatically")
		require.Empty(t, fixture.deployer.requests)
	})
	t.Run("move refused by its own preconditions", func(t *testing.T) {
		fixture := newPlacementFixture(t, nil)
		fixture.deployer.err = errors.New(apiError.InferenceUpgradeConflict)
		outcome := fixture.service.autoPlacementCycle(context.Background())
		require.Contains(t, outcome, "was refused")
		require.Len(t, fixture.deployer.requests, 1)
		// A refused move is not pending and may be retried on a later cycle.
		fixture.deployer.err = nil
		require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "moved model model-a2")
	})
	t.Run("recently moved model", func(t *testing.T) {
		fixture := newPlacementFixture(t, nil)
		require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "moved model model-a2")
		// The deployment failed and left the model in place: it is not
		// redeployed again until the cooldown has passed.
		fixture.clock = fixture.clock.Add(10 * time.Minute)
		require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "still being deployed")
		fixture.clock = fixture.clock.Add(25 * time.Minute)
		require.Contains(t, fixture.service.autoPlacementCycle(context.Background()), "moved model model-a2")
		require.Len(t, fixture.deployer.requests, 2)
	})
}

func TestGPUStatusReportsMeasuredReservationsAndForeignMemory(t *testing.T) {
	fixture := newPlacementFixture(t, func(c *modelmanager.Config) {
		c.PlacementMode = modelmanager.PlacementPinned
		c.ReservationSource = modelmanager.ReservationMeasured
		c.GPUReservationPercent = 150
		c.GPUBudgetsMiB = map[string]int{adminGPUA: 10000, adminGPUB: 10000}
		c.MeasureInterval = 5 * time.Millisecond
	})
	for name := range fixture.api.resources {
		fixture.api.resources[name] = dockerAPI.ModelResources{GPURequired: true, ResidentMemoryMiB: 2000, PeakMemoryMiB: 3467, MaxConcurrentInferences: 1, IdleTimeoutSeconds: 3600}
	}
	require.NoError(t, fixture.manager.LoadMeasurements(context.Background(), &placementStore{rows: []modelmanager.Measurement{
		{ContainerID: placementID(2), PeakMiB: 2374, ResidentMiB: 2300, Samples: 5},
		{ContainerID: placementID(3), PeakMiB: 1898, ResidentMiB: 1800, Samples: 2},
	}}))
	probe := &placementProbe{
		processes: map[string][]dockerSDK.GPUProcess{adminGPUA: {{PID: "20", UsedMemoryMiB: 2350}, {PID: "999", UsedMemoryMiB: 640}}},
		pids:      map[string][]string{placementID(2): {"20"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fixture.manager.RunGPUMeasurement(ctx, probe, nil)
	require.Eventually(t, func() bool { return !fixture.manager.Measurements().GPUs[adminGPUA].SampledAt.IsZero() }, 2*time.Second, time.Millisecond)

	status, err := fixture.service.Snapshot(context.Background(), "tenant-a")
	require.NoError(t, err)
	byID := map[string]GPUModelView{}
	for _, model := range status.Models {
		byID[model.ModelID] = model
	}
	measured := byID["model-a2"]
	require.Equal(t, 3467, *measured.DeclaredPeakMiB)
	require.Equal(t, 5201, *measured.DeclaredReservationMiB)
	require.Equal(t, 3224, *measured.ReservationMiB)
	require.Equal(t, 2374, *measured.MeasuredPeakMiB)
	require.Equal(t, 2300, *measured.MeasuredResidentMiB)
	require.Equal(t, 2350, *measured.MeasuredLastMiB)
	require.Equal(t, 5, measured.MeasuredSamples)
	require.Equal(t, "measured", measured.ReservationSource)
	require.NotNil(t, measured.MeasuredAt)

	learning := byID["model-a3"]
	require.Equal(t, 5201, *learning.ReservationMiB, "below the minimum samples the declaration still applies")
	require.Equal(t, 1898, *learning.MeasuredPeakMiB)
	require.Equal(t, 2, learning.MeasuredSamples)
	require.Equal(t, "declared", learning.ReservationSource)

	// Without a catalog deployment the resources come from the manager's
	// own inventory, so the reservation is still reported.
	pinned := byID["model-a1"]
	require.False(t, pinned.PlacementSupported)
	require.Equal(t, 300, *pinned.DeclaredPeakMiB)
	require.Equal(t, 450, *pinned.ReservationMiB)

	require.Equal(t, adminGPUA, status.Devices[0].UUID)
	require.Equal(t, 640, *status.Devices[0].ForeignMiB)
	require.Equal(t, 2350, *status.Devices[0].ManagedMiB)
	require.NotNil(t, status.Devices[0].ProcessSampledAt)
	require.Equal(t, adminGPUB, status.Devices[1].UUID)
	require.Zero(t, *status.Devices[1].ForeignMiB, "GPU B was sampled and runs nothing")
}
