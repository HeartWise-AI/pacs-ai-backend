package service

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/modelmanager"
	"api-pacs/module/inference/infrastructure/modelupgrade"
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
	// ForeignMiB is sampled memory of processes that belong to no managed
	// model; null until the per-process sampler has a recent sample.
	ForeignMiB       *int       `json:"foreignMiB"`
	ManagedMiB       *int       `json:"managedMiB"`
	ProcessSampledAt *time.Time `json:"processSampledAt"`
}
type GPUModelView struct {
	ModelID            string  `json:"modelId"`
	ContainerID        string  `json:"containerId"`
	GPUUUID            *string `json:"gpuUuid"`
	DesiredGPUUUID     *string `json:"desiredGpuUuid"`
	PlacementSupported bool    `json:"placementSupported"`
	Reason             string  `json:"reason,omitempty"`
	// Reservation fields are null when the model's resource contract is not
	// known; measured fields are null until the sampler has seen the model.
	DeclaredPeakMiB        *int       `json:"declaredPeakMiB"`
	DeclaredReservationMiB *int       `json:"declaredReservationMiB"`
	ReservationMiB         *int       `json:"reservationMiB"`
	MeasuredPeakMiB        *int       `json:"measuredPeakMiB"`
	MeasuredResidentMiB    *int       `json:"measuredResidentMiB"`
	MeasuredLastMiB        *int       `json:"measuredLastMiB"`
	MeasuredSamples        int        `json:"measuredSamples"`
	MeasuredOvershoots     int        `json:"measuredOvershoots"`
	MeasuredAt             *time.Time `json:"measuredAt"`
	ReservationSource      string     `json:"reservationSource,omitempty"`
	GPUModelImageView
}

// GPUModelImageView compares the image a model container runs with the image
// registered for it in the catalog. A move keeps the running image, so
// imageDrift tells an operator that the registration names another one.
type GPUModelImageView struct {
	// ImageDrift is true when the container runs an image other than the
	// registered catalog version (for example a local build deployed by hand).
	ImageDrift      bool   `json:"imageDrift"`
	RunningImage    string `json:"runningImage,omitempty"`
	RunningImageID  string `json:"runningImageId,omitempty"`
	RegisteredImage string `json:"registeredImage,omitempty"`
	// RunningImageUnavailable is true when the running image is no longer
	// present on the host: a move could not keep it and would deploy the
	// registered image, so the automatic loop never moves such a model.
	RunningImageUnavailable bool `json:"runningImageUnavailable,omitempty"`
}

// imageStateMaxAge is how long a model's image comparison is reused.
const imageStateMaxAge = time.Minute

type cachedImageState struct {
	at    time.Time
	value GPUModelImageView
}

// GPUAdminVersionReader reads the catalog version a registration refers to.
type GPUAdminVersionReader interface {
	SelectInferenceModelVersion(context.Context, string) (entity.InferenceModelVersion, error)
}

// processSampleMaxAge is how long a per-process GPU sample is still reported.
const processSampleMaxAge = 2 * time.Minute

func (view *GPUModelView) setReservation(measured modelmanager.ModelMeasurement) {
	if measured.ResourcesKnown {
		view.DeclaredPeakMiB = &measured.DeclaredPeakMiB
		view.DeclaredReservationMiB = &measured.DeclaredReservationMiB
		view.ReservationMiB = &measured.ReservationMiB
		view.ReservationSource = measured.ReservationSource
	}
	view.MeasuredSamples, view.MeasuredOvershoots = measured.Samples, measured.Overshoots
	if measured.Sampled {
		view.MeasuredPeakMiB = &measured.MeasuredPeakMiB
		view.MeasuredResidentMiB = &measured.MeasuredResidentMiB
		view.MeasuredLastMiB = &measured.LastMiB
		view.MeasuredAt = &measured.SampledAt
	}
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
	// Operations is optional: with it the automatic placement loop learns at
	// once that a deployment it started has failed.
	Operations GPUAdminOperations
	// Versions is optional: with it the running image of each model is compared
	// with its catalog version, otherwise with the registration's own snapshot.
	Versions GPUAdminVersionReader
	// Clock is replaced in tests; nil means time.Now.
	Clock  func() time.Time
	mu     sync.Mutex
	cache  map[string]cachedGPUProbe
	images map[string]cachedImageState
	autoMu sync.Mutex
	auto   autoPlacementState
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

// imageState compares what a model container runs with its registration. It
// only reads Docker and the catalog, and is cached because the status is polled.
func (service *GPUAdministrationService) imageState(ctx context.Context, model entity.InferenceModel, info dockerTypes.GetContainerInfoResult) GPUModelImageView {
	inspector, ok := service.Docker.(modelupgrade.ImageInspector)
	if !ok || info.ImageID == "" {
		return GPUModelImageView{}
	}
	versionID := ""
	if model.Deployment != nil {
		versionID = model.Deployment.CatalogVersionID
	}
	key := info.ID + "/" + info.ImageID + "/" + versionID
	service.mu.Lock()
	cached, hit := service.images[key]
	service.mu.Unlock()
	if hit && time.Since(cached.at) < imageStateMaxAge {
		return cached.value
	}
	var version *entity.InferenceModelVersion
	if service.Versions != nil && versionID != "" {
		if found, err := service.Versions.SelectInferenceModelVersion(ctx, versionID); err == nil {
			found.Normalize()
			version = &found
		}
	}
	running := modelupgrade.ResolveRunningImage(ctx, inspector, info, model, version)
	view := GPUModelImageView{
		ImageDrift: running.Drift, RunningImage: running.Reference, RunningImageID: running.ImageID,
		RegisteredImage: running.RegisteredReference, RunningImageUnavailable: !running.Available,
	}
	service.mu.Lock()
	if service.images == nil || len(service.images) > 256 {
		service.images = map[string]cachedImageState{}
	}
	service.images[key] = cachedImageState{at: time.Now(), value: view}
	service.mu.Unlock()
	return view
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
	measurements := modelmanager.MeasurementReport{}
	if service.Manager != nil {
		snapshot = service.Manager.Snapshot()
		measurements = service.Manager.Measurements()
		result.ManagerEnabled = true
		result.ManagerReady = snapshot.Ready
	}
	probes := map[string]string{}
	for _, model := range models {
		if model.TenantID != tenantID {
			continue
		}
		view := GPUModelView{ModelID: model.ID, ContainerID: model.ContainerID, DesiredGPUUUID: optionalGPUUUID(model.PreferredGPUUUID)}
		// What the manager already knows; replaced below when the running
		// container reports its resource contract.
		if known, ok := measurements.Models[model.ContainerID]; ok {
			view.setReservation(known)
		}
		info, inspectErr := service.Docker.GetContainerInfo(ctx, model.ContainerID)
		if inspectErr != nil || info.ID != model.ContainerID {
			view.Reason = "Container is unavailable"
		} else {
			view.GPUUUID = optionalGPUUUID(info.GPUUUID)
			view.GPUModelImageView = service.imageState(ctx, model, info)
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
				view.setReservation(service.Manager.ModelReservation(model.ContainerID, info.GPUUUID, metadata.Data.Resources))
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
		if usage, ok := measurements.GPUs[uuid]; ok && time.Since(usage.SampledAt) < processSampleMaxAge {
			view.ForeignMiB, view.ManagedMiB, view.ProcessSampledAt = &usage.ForeignMiB, &usage.ManagedMiB, &usage.SampledAt
		}
		result.Devices = append(result.Devices, view)
	}
	return result, nil
}

// move recreates a model's container on another GPU from the image and
// configuration of the container that runs now, not from the catalog version:
// a model deployed by hand from a newer or local image must not be downgraded
// by being moved. automatic is true for the placement loop.
func (service *GPUAdministrationService) move(ctx context.Context, tenantID, actor, modelID, uuid, expectedContainerID string, automatic bool) (serviceTypes.DeployInferenceModelVersionResult, error) {
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
	image := service.imageState(ctx, model, info)
	source := modelupgrade.ImageSourceRunning
	if image.RunningImageUnavailable {
		// Without the running image the container can only be recreated from
		// the registered one. The automatic loop never does that.
		if automatic {
			log.Printf("[gpu-placement] move of model=%s refused: the image it runs (%s) is not present on the host and the registered image %q would be deployed instead", modelID, image.RunningImageID, image.RegisteredImage)
			return empty, errors.New(apiError.InferenceUpgradeConflict)
		}
		source = modelupgrade.ImageSourceRegistered
	}
	log.Printf("[gpu-placement] move of model=%s container=%.12s to=%s automatic=%t image_source=%s running_image=%q running_image_id=%s registered_image=%q image_drift=%t",
		modelID, expectedContainerID, uuid, automatic, source, image.RunningImage, image.RunningImageID, image.RegisteredImage, image.ImageDrift)
	return service.Deployer.DeployInferenceModelVersion(ctx, serviceTypes.DeployInferenceModelVersion{
		TenantID: tenantID, ActorUserID: actor, ModelID: modelID, VersionID: model.Deployment.CatalogVersionID,
		TargetGPUUUID: uuid, ExpectedContainerID: expectedContainerID, KeepRunningImage: true, Automatic: automatic,
	})
}
