package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/modelmanager"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type GPUDeviceView struct {
	UUID                string     `json:"uuid"`
	Index               *int       `json:"index"`
	Name                *string    `json:"name"`
	TotalMemoryMiB      *int       `json:"totalMemoryMiB"`
	UsedMemoryMiB       *int       `json:"usedMemoryMiB"`
	FreeMemoryMiB       *int       `json:"freeMemoryMiB"`
	UtilizationPercent  *int       `json:"utilizationPercent"`
	TelemetryStatus     string     `json:"telemetryStatus"`
	TelemetrySampledAt  *time.Time `json:"telemetrySampledAt"`
	ManagerCapacityMiB  int        `json:"managerCapacityMiB"`
	ManagerReservedMiB  int        `json:"managerReservedMiB"`
	ManagerAvailableMiB int        `json:"managerAvailableMiB"`
}
type GPUModelView struct {
	ModelID            string  `json:"modelId"`
	ContainerID        string  `json:"containerId"`
	GPUUUID            *string `json:"gpuUuid"`
	DesiredGPUUUID     *string `json:"desiredGpuUuid"`
	PlacementSupported bool    `json:"placementSupported"`
	Reason             string  `json:"reason,omitempty"`
}
type GPUAdministrationSnapshot struct {
	SampledAt      time.Time       `json:"sampledAt"`
	ManagerEnabled bool            `json:"managerEnabled"`
	ManagerReady   bool            `json:"managerReady"`
	Devices        []GPUDeviceView `json:"devices"`
	Models         []GPUModelView  `json:"models"`
}
type GPUAdminDocker interface {
	GetContainerInfo(context.Context, string) (dockerTypes.GetContainerInfoResult, error)
	ReadGPUTelemetry(context.Context, string, string) (dockerSDK.GPUTelemetry, error)
}
type GPUAdminModelAPI interface {
	GetModelInfo(context.Context, string) (dockerAPI.GetModelInfoResponse, error)
}
type GPUAdminDeployer interface {
	DeployInferenceModelVersion(context.Context, serviceTypes.DeployInferenceModelVersion) (serviceTypes.DeployInferenceModelVersionResult, error)
}
type cachedGPUProbe struct {
	at    time.Time
	value dockerSDK.GPUTelemetry
	err   error
}

// GPUAdministrationService exposes hardware counters separately from admission
// reservations, and restricts model actions/probe containers to one tenant.
type GPUAdministrationService struct {
	Manager  *modelmanager.Manager
	Models   repository.InferenceQueryRepositoryInterface
	Docker   GPUAdminDocker
	API      GPUAdminModelAPI
	Deployer GPUAdminDeployer
	mu       sync.Mutex
	cache    map[string]cachedGPUProbe
}

func optionalGPUUUID(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func (service *GPUAdministrationService) sample(ctx context.Context, id, uuid string) (dockerSDK.GPUTelemetry, error) {
	// Serialize refreshes and cache failures as well as successes to bound Docker
	// exec attempts during polling. Keys include tenant-authorized container IDs.
	service.mu.Lock()
	defer service.mu.Unlock()
	key := id + "/" + uuid
	if cached, ok := service.cache[key]; ok && time.Since(cached.at) < 5*time.Second {
		return cached.value, cached.err
	}
	value, err := service.Docker.ReadGPUTelemetry(ctx, id, uuid)
	if service.cache == nil {
		service.cache = map[string]cachedGPUProbe{}
	}
	if len(service.cache) > 128 {
		service.cache = map[string]cachedGPUProbe{}
	}
	service.cache[key] = cachedGPUProbe{at: time.Now(), value: value, err: err}
	return value, err
}

func (service *GPUAdministrationService) Snapshot(ctx context.Context, tenantID string) (GPUAdministrationSnapshot, error) {
	result := GPUAdministrationSnapshot{SampledAt: time.Now().UTC(), Devices: []GPUDeviceView{}, Models: []GPUModelView{}}
	if strings.TrimSpace(tenantID) == "" || service.Models == nil {
		return result, errors.New(apiError.MissingRecord)
	}
	models, err := service.Models.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return result, err
	}
	if err != nil {
		models = nil
	}
	snapshot := modelmanager.Snapshot{GPUs: map[string]modelmanager.GPUSnapshot{}}
	if service.Manager != nil {
		snapshot = service.Manager.Snapshot()
		result.ManagerEnabled = true
		result.ManagerReady = snapshot.Ready
	}
	probes := map[string]string{}
	for _, model := range models {
		if model.TenantID != tenantID {
			continue
		}
		view := GPUModelView{ModelID: model.ID, ContainerID: model.ContainerID, DesiredGPUUUID: optionalGPUUUID(model.PreferredGPUUUID)}
		info, inspectErr := service.Docker.GetContainerInfo(ctx, model.ContainerID)
		if inspectErr != nil || info.ID != model.ContainerID {
			view.Reason = "Container is unavailable"
		} else {
			view.GPUUUID = optionalGPUUUID(info.GPUUUID)
			if info.GPUUUID != "" && info.Lifecycle == dockerTypes.LifecycleRunning {
				if _, exists := probes[info.GPUUUID]; !exists {
					probes[info.GPUUUID] = info.ID
				}
			}
			if service.Manager == nil || !snapshot.Ready || len(snapshot.GPUs) == 0 {
				view.Reason = "Per-GPU admission is unavailable"
			} else if info.GPUUUID == "" {
				view.Reason = "Container has no explicit GPU placement"
			} else if info.Lifecycle != dockerTypes.LifecycleRunning {
				view.Reason = "Start the container before changing GPU placement"
			} else if model.Deployment == nil || model.Deployment.CatalogVersionID == "" {
				view.Reason = "An immutable catalog deployment is required"
			} else if service.API == nil {
				view.Reason = "Model resource metadata is unavailable"
			} else if metadata, err := service.API.GetModelInfo(ctx, strings.TrimPrefix(info.Name, "/")); err != nil || metadata.Data.Resources.Validate() != nil {
				view.Reason = "Model resource metadata is unavailable"
			} else if !metadata.Data.Resources.GPURequired {
				view.Reason = "This model does not require a GPU"
			} else {
				view.PlacementSupported = true
			}
		}
		result.Models = append(result.Models, view)
	}
	uuids := make([]string, 0, len(snapshot.GPUs))
	for uuid := range snapshot.GPUs {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)
	for _, uuid := range uuids {
		ledger := snapshot.GPUs[uuid]
		view := GPUDeviceView{UUID: uuid, TelemetryStatus: "unavailable", ManagerCapacityMiB: ledger.CapacityMiB, ManagerReservedMiB: ledger.ReservedMiB, ManagerAvailableMiB: ledger.AvailableMiB}
		if id := probes[uuid]; id != "" {
			measured, sampleErr := service.sample(ctx, id, uuid)
			if sampleErr == nil {
				// Pinned containers renumber NVML devices locally; there is no
				// authoritative host ordinal here. UUID is the physical identity.
				view.Index = nil
				view.Name = &measured.Name
				view.TotalMemoryMiB = &measured.TotalMemoryMiB
				view.UsedMemoryMiB = &measured.UsedMemoryMiB
				view.FreeMemoryMiB = &measured.FreeMemoryMiB
				view.UtilizationPercent = &measured.UtilizationPercent
				view.TelemetryStatus = "available"
				view.TelemetrySampledAt = &measured.SampledAt
			}
		}
		result.Devices = append(result.Devices, view)
	}
	return result, nil
}

func (service *GPUAdministrationService) Move(ctx context.Context, tenantID, actor, modelID, uuid, expectedContainerID string) (serviceTypes.DeployInferenceModelVersionResult, error) {
	empty := serviceTypes.DeployInferenceModelVersionResult{}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(actor) == "" || strings.TrimSpace(modelID) == "" || strings.TrimSpace(uuid) == "" || strings.TrimSpace(expectedContainerID) == "" {
		return empty, errors.New(apiError.InvalidPayload)
	}
	if service.Manager == nil || !service.Manager.Snapshot().Ready || service.Deployer == nil {
		return empty, errors.New(apiError.InferenceUpgradeUnavailable)
	}
	model, err := service.Models.SelectInferenceModelByID(ctx, modelID)
	if err != nil {
		return empty, err
	}
	if model.TenantID != tenantID {
		return empty, errors.New(apiError.MissingRecord)
	}
	if model.ContainerID != expectedContainerID {
		return empty, errors.New(apiError.InferenceUpgradeConflict)
	}
	if model.Deployment == nil || model.Deployment.CatalogVersionID == "" {
		return empty, errors.New(apiError.InvalidPayload)
	}
	info, err := service.Docker.GetContainerInfo(ctx, model.ContainerID)
	if err != nil || info.ID != model.ContainerID || info.Lifecycle != dockerTypes.LifecycleRunning {
		return empty, errors.New(apiError.InferenceUpgradeUnavailable)
	}
	if info.GPUUUID == "" {
		return empty, errors.New(apiError.InvalidPayload)
	}
	if info.GPUUUID == uuid {
		return empty, errors.New(apiError.InvalidPayload)
	}
	metadata, err := service.API.GetModelInfo(ctx, strings.TrimPrefix(info.Name, "/"))
	if err != nil {
		return empty, errors.New(apiError.InferenceUpgradeUnavailable)
	}
	if err := service.Manager.ValidateGPUPlacement(uuid, metadata.Data.Resources); err != nil {
		return empty, errors.New(apiError.InvalidPayload)
	}
	return service.Deployer.DeployInferenceModelVersion(ctx, serviceTypes.DeployInferenceModelVersion{TenantID: tenantID, ActorUserID: actor, ModelID: modelID, VersionID: model.Deployment.CatalogVersionID, TargetGPUUUID: uuid, ExpectedContainerID: expectedContainerID})
}
