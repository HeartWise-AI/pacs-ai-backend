package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/modelmanager"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

const adminGPUA = "GPU-11111111-1111-1111-1111-111111111111"
const adminGPUB = "GPU-22222222-2222-2222-2222-222222222222"

type adminTestBackend struct{ modelmanager.Backend }
type adminTestModels struct {
	domainRepository.InferenceQueryRepositoryInterface
	models []entity.InferenceModel
	err    error
}

func (m *adminTestModels) SelectInferenceModels(context.Context, string) ([]entity.InferenceModel, error) {
	return m.models, m.err
}
func (m *adminTestModels) SelectInferenceModelByID(_ context.Context, id string) (entity.InferenceModel, error) {
	if m.err != nil {
		return entity.InferenceModel{}, m.err
	}
	for _, model := range m.models {
		if model.ID == id {
			return model, nil
		}
	}
	return entity.InferenceModel{}, errors.New("missing")
}

type adminTestDocker struct {
	infos       map[string]dockerTypes.GetContainerInfoResult
	inspected   []string
	probed      []string
	measurement dockerSDK.GPUTelemetry
	probeErr    error
}

func (d *adminTestDocker) GetContainerInfo(_ context.Context, id string) (dockerTypes.GetContainerInfoResult, error) {
	d.inspected = append(d.inspected, id)
	if info, ok := d.infos[id]; ok {
		return info, nil
	}
	return dockerTypes.GetContainerInfoResult{}, errors.New("missing container")
}
func (d *adminTestDocker) ReadGPUTelemetry(_ context.Context, id, uuid string) (dockerSDK.GPUTelemetry, error) {
	d.probed = append(d.probed, id+"/"+uuid)
	return d.measurement, d.probeErr
}

type adminTestAPI struct {
	info  dockerAPI.GetModelInfoResponse
	err   error
	names []string
}

func (a *adminTestAPI) GetModelInfo(_ context.Context, name string) (dockerAPI.GetModelInfoResponse, error) {
	a.names = append(a.names, name)
	return a.info, a.err
}

type adminTestDeployer struct {
	requests []serviceTypes.DeployInferenceModelVersion
	err      error
}

func (d *adminTestDeployer) DeployInferenceModelVersion(_ context.Context, req serviceTypes.DeployInferenceModelVersion) (serviceTypes.DeployInferenceModelVersionResult, error) {
	d.requests = append(d.requests, req)
	return serviceTypes.DeployInferenceModelVersionResult{}, d.err
}

func adminServiceFixture(t *testing.T) (*GPUAdministrationService, *adminTestModels, *adminTestDocker, *adminTestAPI, *adminTestDeployer) {
	t.Helper()
	config := modelmanager.Config{Enabled: true, RequireReconciliation: true, RetainPeakWhileLoaded: true, GPUReservationPercent: 150, GPUBudgetsMiB: map[string]int{adminGPUA: 1000, adminGPUB: 1000}, SafetyMarginMiB: 100, QueueTimeout: time.Second, OperationTimeout: time.Second}
	manager, err := modelmanager.New(config, &adminTestBackend{})
	require.NoError(t, err)
	require.NoError(t, manager.Reconcile(context.Background(), nil))
	id := strings.Repeat("a", 64)
	models := &adminTestModels{models: []entity.InferenceModel{{ID: "model-a", TenantID: "tenant-a", ContainerID: id, PreferredGPUUUID: adminGPUA, Deployment: &entity.InferenceModelDeployment{CatalogVersionID: "artifact-v1", ModelVersion: "1.0.0"}}}}
	docker := &adminTestDocker{infos: map[string]dockerTypes.GetContainerInfoResult{id: {ID: id, Name: "/model-a", GPUUUID: adminGPUA, Lifecycle: dockerTypes.LifecycleRunning}}, measurement: dockerSDK.GPUTelemetry{UUID: adminGPUA, Index: 1, Name: "test GPU", TotalMemoryMiB: 24000, UsedMemoryMiB: 700, FreeMemoryMiB: 23300, UtilizationPercent: 35, SampledAt: time.Now().UTC()}}
	api := &adminTestAPI{}
	api.info.Data.Resources = dockerAPI.ModelResources{GPURequired: true, ResidentMemoryMiB: 50, PeakMemoryMiB: 100, MaxConcurrentInferences: 1, IdleTimeoutSeconds: 1}
	deployer := &adminTestDeployer{}
	return &GPUAdministrationService{Manager: manager, Models: models, Docker: docker, API: api, Deployer: deployer}, models, docker, api, deployer
}

func TestGPUAdminSnapshotSeparatesPhysicalCountersAndAdmissionReservations(t *testing.T) {
	service, models, docker, _, _ := adminServiceFixture(t)
	otherID := strings.Repeat("b", 64)
	models.models = append(models.models, entity.InferenceModel{ID: "other-tenant", TenantID: "tenant-b", ContainerID: otherID})
	docker.infos[otherID] = dockerTypes.GetContainerInfoResult{ID: otherID, Name: "/private", GPUUUID: adminGPUB, Lifecycle: dockerTypes.LifecycleRunning}
	snapshot, err := service.Snapshot(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.True(t, snapshot.ManagerEnabled)
	require.True(t, snapshot.ManagerReady)
	require.Len(t, snapshot.Models, 1)
	require.Equal(t, "model-a", snapshot.Models[0].ModelID)
	require.True(t, snapshot.Models[0].PlacementSupported)
	require.Equal(t, adminGPUA, *snapshot.Models[0].DesiredGPUUUID)
	require.NotContains(t, docker.inspected, otherID)
	require.Equal(t, []string{models.models[0].ContainerID + "/" + adminGPUA}, docker.probed)
	require.Len(t, snapshot.Devices, 2)
	physical := snapshot.Devices[0]
	require.Nil(t, physical.Index, "a container-local ordinal is not a physical GPU index")
	require.Equal(t, adminGPUA, physical.UUID)
	require.Equal(t, "available", physical.TelemetryStatus)
	require.Equal(t, 700, *physical.UsedMemoryMiB)
	require.Equal(t, 23300, *physical.FreeMemoryMiB)
	require.Equal(t, 24000, *physical.TotalMemoryMiB)
	require.Equal(t, 35, *physical.UtilizationPercent)
	require.Equal(t, 900, physical.ManagerCapacityMiB)
	require.Zero(t, physical.ManagerReservedMiB)
	require.Equal(t, 900, physical.ManagerAvailableMiB)
	require.Equal(t, docker.measurement.SampledAt, *physical.TelemetrySampledAt)
	unavailable := snapshot.Devices[1]
	require.Equal(t, "unavailable", unavailable.TelemetryStatus)
	require.Nil(t, unavailable.UsedMemoryMiB)
	require.Nil(t, unavailable.FreeMemoryMiB)
	require.Nil(t, unavailable.UtilizationPercent)
	require.Nil(t, unavailable.TelemetrySampledAt)
	require.Equal(t, 900, unavailable.ManagerAvailableMiB)
}

func TestGPUAdminSnapshotCachesSuccessAndFailureForFiveSeconds(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			service, models, docker, _, _ := adminServiceFixture(t)
			if failed {
				docker.probeErr = errors.New("probe unavailable")
			}
			for i := 0; i < 2; i++ {
				snapshot, err := service.Snapshot(context.Background(), "tenant-a")
				require.NoError(t, err)
				if failed {
					require.Equal(t, "unavailable", snapshot.Devices[0].TelemetryStatus)
					require.Nil(t, snapshot.Devices[0].UsedMemoryMiB)
				}
			}
			require.Len(t, docker.probed, 1)
			key := models.models[0].ContainerID + "/" + adminGPUA
			service.mu.Lock()
			cached := service.cache[key]
			cached.at = time.Now().Add(-6 * time.Second)
			service.cache[key] = cached
			service.mu.Unlock()
			docker.probeErr = nil
			snapshot, err := service.Snapshot(context.Background(), "tenant-a")
			require.NoError(t, err)
			require.Len(t, docker.probed, 2)
			require.Equal(t, "available", snapshot.Devices[0].TelemetryStatus)
		})
	}
}

func TestGPUAdminSnapshotNeverStartsStoppedModelsAndRejectsMissingTenant(t *testing.T) {
	service, models, docker, _, _ := adminServiceFixture(t)
	info := docker.infos[models.models[0].ContainerID]
	info.Lifecycle = dockerTypes.LifecycleStopped
	docker.infos[info.ID] = info
	snapshot, err := service.Snapshot(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Empty(t, docker.probed)
	require.False(t, snapshot.Models[0].PlacementSupported)
	require.Equal(t, "unavailable", snapshot.Devices[0].TelemetryStatus)
	_, err = service.Snapshot(context.Background(), "")
	require.Error(t, err)
}

func TestGPUAdminSnapshotHandlesEmptyTenantRegistryWithoutProbingOtherTenants(t *testing.T) {
	service, models, docker, _, _ := adminServiceFixture(t)
	models.err = errors.New(apiError.MissingRecord)
	snapshot, err := service.Snapshot(context.Background(), "tenant-empty")
	require.NoError(t, err)
	require.Empty(t, snapshot.Models)
	require.Len(t, snapshot.Devices, 2)
	require.Empty(t, docker.inspected)
	require.Empty(t, docker.probed)
	for _, device := range snapshot.Devices {
		require.Equal(t, "unavailable", device.TelemetryStatus)
		require.Nil(t, device.UsedMemoryMiB)
	}
	models.err = errors.New("database unavailable")
	_, err = service.Snapshot(context.Background(), "tenant-empty")
	require.Error(t, err)
}

func TestGPUAdminSnapshotDisablesPlacementWithoutValidatedGPUContract(t *testing.T) {
	for _, scenario := range []string{"CPU model", "invalid resources", "metadata unavailable", "missing catalog", "unplaced container", "wrong container identity", "manager unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			service, models, docker, api, _ := adminServiceFixture(t)
			switch scenario {
			case "CPU model":
				api.info.Data.Resources.GPURequired = false
				api.info.Data.Resources.ResidentMemoryMiB = 0
				api.info.Data.Resources.PeakMemoryMiB = 0
			case "invalid resources":
				api.info.Data.Resources.MaxConcurrentInferences = 0
			case "metadata unavailable":
				api.err = errors.New("unavailable")
			case "missing catalog":
				models.models[0].Deployment = nil
			case "unplaced container":
				id := models.models[0].ContainerID
				info := docker.infos[id]
				info.GPUUUID = ""
				docker.infos[id] = info
			case "manager unavailable":
				service.Manager = nil
			case "wrong container identity":
				id := models.models[0].ContainerID
				info := docker.infos[id]
				info.ID = strings.Repeat("b", 64)
				docker.infos[id] = info
			}
			snapshot, err := service.Snapshot(context.Background(), "tenant-a")
			require.NoError(t, err)
			require.False(t, snapshot.Models[0].PlacementSupported)
			require.NotEmpty(t, snapshot.Models[0].Reason)
			if scenario == "wrong container identity" {
				require.Empty(t, docker.probed)
			}
		})
	}
}

func TestGPUAdminMoveKeepsArtifactAndPropagatesCompareAndSwapIdentity(t *testing.T) {
	service, models, _, api, deployer := adminServiceFixture(t)
	before := models.models[0]
	_, err := service.Move(context.Background(), "tenant-a", "operator", "model-a", adminGPUB, before.ContainerID)
	require.NoError(t, err)
	require.Equal(t, []string{"model-a"}, api.names)
	require.Equal(t, []serviceTypes.DeployInferenceModelVersion{{TenantID: "tenant-a", ActorUserID: "operator", ModelID: "model-a", VersionID: "artifact-v1", TargetGPUUUID: adminGPUB, ExpectedContainerID: before.ContainerID}}, deployer.requests)
	require.Equal(t, before, models.models[0])
}

func TestGPUAdminMoveRejectsUnauthorizedStaleAndUnsupportedRequests(t *testing.T) {
	for _, scenario := range []string{"cross tenant", "stale container", "unknown GPU", "invalid UUID", "same GPU", "missing catalog", "CPU model", "oversized peak", "invalid resources", "metadata unavailable", "missing container", "wrong container identity", "stopped container", "unplaced container", "manager unavailable", "manager not ready", "missing actor", "missing model", "missing tenant", "missing expected container"} {
		t.Run(scenario, func(t *testing.T) {
			service, models, docker, api, deployer := adminServiceFixture(t)
			tenant, actor, modelID, target, expected := "tenant-a", "operator", "model-a", adminGPUB, models.models[0].ContainerID
			switch scenario {
			case "cross tenant":
				tenant = "tenant-b"
			case "stale container":
				expected = strings.Repeat("f", 64)
			case "unknown GPU":
				target = "GPU-33333333-3333-3333-3333-333333333333"
			case "invalid UUID":
				target = "0"
			case "same GPU":
				target = adminGPUA
			case "missing catalog":
				models.models[0].Deployment = nil
			case "CPU model":
				api.info.Data.Resources.GPURequired = false
				api.info.Data.Resources.ResidentMemoryMiB = 0
				api.info.Data.Resources.PeakMemoryMiB = 0
			case "oversized peak":
				api.info.Data.Resources.PeakMemoryMiB = 700 // 150% > target's 900 MiB capacity.
			case "invalid resources":
				api.info.Data.Resources.MaxConcurrentInferences = 0
			case "metadata unavailable":
				api.err = errors.New("offline")
			case "missing container":
				docker.infos = nil
			case "wrong container identity", "stopped container", "unplaced container":
				info := docker.infos[expected]
				if scenario == "wrong container identity" {
					info.ID = strings.Repeat("b", 64)
				}
				if scenario == "stopped container" {
					info.Lifecycle = dockerTypes.LifecycleStopped
				}
				if scenario == "unplaced container" {
					info.GPUUUID = ""
				}
				docker.infos[expected] = info
			case "manager unavailable":
				service.Manager = nil
			case "manager not ready":
				var err error
				service.Manager, err = modelmanager.New(modelmanager.Config{Enabled: true, RequireReconciliation: true, BudgetMiB: 1000, QueueTimeout: time.Second, OperationTimeout: time.Second}, &adminTestBackend{})
				require.NoError(t, err)
			case "missing actor":
				actor = ""
			case "missing model":
				modelID = "missing"
			case "missing tenant":
				tenant = ""
			case "missing expected container":
				expected = ""
			}
			_, err := service.Move(context.Background(), tenant, actor, modelID, target, expected)
			require.Error(t, err)
			require.Empty(t, deployer.requests)
			if scenario == "cross tenant" || scenario == "stale container" {
				require.Empty(t, docker.inspected)
				require.Empty(t, api.names)
			}
		})
	}
}
