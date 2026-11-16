package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

// ErrGPUPlacementPinned is returned while MODEL_GPU_PLACEMENT=pinned.
var ErrGPUPlacementPinned = errors.New("GPU placement planning is disabled")

type GPUPlacementMoveView struct {
	ModelID        string `json:"modelId"`
	ContainerID    string `json:"containerId"`
	FromGPUUUID    string `json:"fromGpuUuid"`
	ToGPUUUID      string `json:"toGpuUuid"`
	ReservationMiB int    `json:"reservationMiB"`
	// BlockedReason says why the automatic loop would not apply this move now.
	BlockedReason string `json:"blockedReason,omitempty"`
}
type GPUPlacementDeviceView struct {
	UUID        string `json:"uuid"`
	Allowed     bool   `json:"allowed"`
	CapacityMiB int    `json:"capacityMiB"`
	// OtherReservedMiB is held for models outside this workspace; it is
	// subtracted from the capacity the plan may use.
	OtherReservedMiB  int `json:"otherReservedMiB"`
	ReservedBeforeMiB int `json:"reservedBeforeMiB"`
	ReservedAfterMiB  int `json:"reservedAfterMiB"`
	ModelsBefore      int `json:"modelsBefore"`
	ModelsAfter       int `json:"modelsAfter"`
	ModelsFitBefore   int `json:"modelsFitBefore"`
	ModelsFitAfter    int `json:"modelsFitAfter"`
}
type GPUPlacementModelView struct {
	ModelID         string `json:"modelId"`
	ContainerID     string `json:"containerId"`
	CurrentGPUUUID  string `json:"currentGpuUuid"`
	ProposedGPUUUID string `json:"proposedGpuUuid"`
	ReservationMiB  int    `json:"reservationMiB"`
	Movable         bool   `json:"movable"`
	Reason          string `json:"reason,omitempty"`
}
type GPUPlacementSkippedView struct {
	ModelID string `json:"modelId"`
	Reason  string `json:"reason"`
}
type GPUPlacementAutoView struct {
	Enabled bool `json:"enabled"`
	// BlockedReason says why the automatic loop would apply nothing now.
	BlockedReason string     `json:"blockedReason,omitempty"`
	LastRunAt     *time.Time `json:"lastRunAt"`
	LastOutcome   string     `json:"lastOutcome,omitempty"`
	LastMoveAt    *time.Time `json:"lastMoveAt"`
	LastMoveModel string     `json:"lastMoveModelId,omitempty"`
}
type GPUPlacementPlan struct {
	SampledAt         time.Time                 `json:"sampledAt"`
	Mode              string                    `json:"mode"`
	ReservationSource string                    `json:"reservationSource"`
	Strategy          string                    `json:"strategy"`
	Current           map[string]string         `json:"current"`
	Proposed          map[string]string         `json:"proposed"`
	Moves             []GPUPlacementMoveView    `json:"moves"`
	Devices           []GPUPlacementDeviceView  `json:"devices"`
	Models            []GPUPlacementModelView   `json:"models"`
	Skipped           []GPUPlacementSkippedView `json:"skipped"`
	ModelsTotal       int                       `json:"modelsTotal"`
	ModelsFitBefore   int                       `json:"modelsFitBefore"`
	ModelsFitAfter    int                       `json:"modelsFitAfter"`
	Improves          bool                      `json:"improves"`
	Auto              GPUPlacementAutoView      `json:"auto"`
}

type autoPlacementState struct {
	lastRunAt       time.Time
	lastOutcome     string
	pendingModel    string // model whose automatic move may still be deploying
	pendingSource   string // the container it was moved away from
	pendingAt       time.Time
	lastMoveAt      map[string]time.Time
	lastMovedModel  string
	lastMovedModelT time.Time
}

func (service *GPUAdministrationService) now() time.Time {
	if service.Clock != nil {
		return service.Clock()
	}
	return time.Now()
}

// autoMoveCooldown keeps a model from being redeployed again soon after an
// automatic move, and bounds how long an unfinished move blocks the next one.
func autoMoveCooldown(interval time.Duration) time.Duration {
	return max(6*interval, 30*time.Minute)
}

// PlacementPlan computes, without side effects, where the workspace's models
// should run so that as many reservations as possible fit at the same time.
func (service *GPUAdministrationService) PlacementPlan(ctx context.Context, tenantID string) (GPUPlacementPlan, error) {
	plan := GPUPlacementPlan{Current: map[string]string{}, Proposed: map[string]string{}, Moves: []GPUPlacementMoveView{}, Devices: []GPUPlacementDeviceView{}, Models: []GPUPlacementModelView{}, Skipped: []GPUPlacementSkippedView{}}
	if service.Manager == nil {
		return plan, errors.New(apiError.InferenceUpgradeUnavailable)
	}
	config := service.Manager.Settings()
	if config.PlacementMode != modelmanager.PlacementPropose && config.PlacementMode != modelmanager.PlacementAuto {
		return plan, ErrGPUPlacementPinned
	}
	status, err := service.Snapshot(ctx, tenantID)
	if err != nil {
		return plan, err
	}
	// Read the ledger after the status so that a model seen as idle there is
	// checked against a state that is at least as recent.
	ledger := service.Manager.Snapshot()
	if !ledger.Ready || len(ledger.GPUs) == 0 {
		return plan, errors.New(apiError.InferenceUpgradeUnavailable)
	}
	measurements := service.Manager.Measurements()
	now := service.now()
	plan.SampledAt, plan.Mode, plan.ReservationSource = now.UTC(), config.PlacementMode, config.ReservationSource

	allowed := map[string]bool{}
	for _, uuid := range config.AllowedGPUUUIDs {
		allowed[uuid] = true
	}
	tenantContainers := map[string]struct{}{}
	for _, model := range status.Models {
		tenantContainers[model.ContainerID] = struct{}{}
	}
	// Reservations of models registered elsewhere are fixed load on a device.
	other := map[string]int{}
	for id, model := range measurements.Models {
		if _, mine := tenantContainers[id]; mine || !model.GPURequired {
			continue
		}
		if state := ledger.Models[id]; state.Blocked && state.ReservedMiB == 0 && !state.Loaded {
			continue // tombstone of a replaced container
		}
		other[model.GPUUUID] += max(model.ReservationMiB, ledger.Models[id].ReservedMiB)
	}
	gpus := make([]modelmanager.PlacementGPU, 0, len(status.Devices))
	for _, device := range status.Devices {
		gpus = append(gpus, modelmanager.PlacementGPU{UUID: device.UUID, CapacityMiB: max(0, device.ManagerCapacityMiB-other[device.UUID]), Allowed: len(allowed) == 0 || allowed[device.UUID]})
	}

	views := map[string]GPUPlacementModelView{}
	models := make([]modelmanager.PlacementModel, 0, len(status.Models))
	for _, model := range status.Models {
		reason := ""
		switch {
		case model.GPUUUID == nil:
			reason = model.Reason
			if reason == "" {
				reason = "Container has no explicit GPU placement"
			}
		case model.ReservationMiB == nil && ledger.Models[model.ContainerID].ReservedMiB == 0:
			reason = "The model's GPU reservation is not known"
		case model.ReservationMiB != nil && *model.ReservationMiB == 0 && ledger.Models[model.ContainerID].ReservedMiB == 0:
			reason = "This model does not require a GPU"
		default:
			if _, configured := ledger.GPUs[*model.GPUUUID]; !configured {
				reason = "The model's GPU is not managed"
			}
		}
		if reason != "" {
			plan.Skipped = append(plan.Skipped, GPUPlacementSkippedView{ModelID: model.ModelID, Reason: reason})
			continue
		}
		reservation := ledger.Models[model.ContainerID].ReservedMiB
		if model.ReservationMiB != nil {
			reservation = *model.ReservationMiB
		}
		views[model.ModelID] = GPUPlacementModelView{ModelID: model.ModelID, ContainerID: model.ContainerID, CurrentGPUUUID: *model.GPUUUID, ReservationMiB: reservation, Movable: model.PlacementSupported, Reason: model.Reason}
		models = append(models, modelmanager.PlacementModel{ID: model.ModelID, GPUUUID: *model.GPUUUID, ReservationMiB: reservation, Movable: model.PlacementSupported})
	}

	computed := modelmanager.PlanPlacement(gpus, models, 0)
	plan.Strategy, plan.Current, plan.Proposed = computed.Strategy, computed.Current, computed.Proposed
	plan.ModelsTotal, plan.ModelsFitBefore, plan.ModelsFitAfter, plan.Improves = computed.Models, computed.FitBefore, computed.FitAfter, computed.Improves
	for _, device := range computed.GPUs {
		plan.Devices = append(plan.Devices, GPUPlacementDeviceView{
			UUID: device.UUID, Allowed: len(allowed) == 0 || allowed[device.UUID], CapacityMiB: device.CapacityMiB, OtherReservedMiB: other[device.UUID],
			ReservedBeforeMiB: device.ReservedBeforeMiB, ReservedAfterMiB: device.ReservedAfterMiB, ModelsBefore: device.ModelsBefore, ModelsAfter: device.ModelsAfter,
			ModelsFitBefore: device.FitBefore, ModelsFitAfter: device.FitAfter,
		})
	}
	for id, view := range views {
		view.ProposedGPUUUID = computed.Proposed[id]
		plan.Models = append(plan.Models, view)
	}
	sort.Slice(plan.Models, func(i, j int) bool { return plan.Models[i].ModelID < plan.Models[j].ModelID })

	service.autoMu.Lock()
	defer service.autoMu.Unlock()
	cooldown := autoMoveCooldown(config.PlacementInterval)
	for _, move := range computed.Moves {
		view := GPUPlacementMoveView{ModelID: move.ModelID, ContainerID: views[move.ModelID].ContainerID, FromGPUUUID: move.From, ToGPUUUID: move.To, ReservationMiB: move.ReservationMiB}
		view.BlockedReason = moveBlockedReason(ledger.Models, view.ContainerID, config.MinResidency, now)
		if moved, ok := service.auto.lastMoveAt[move.ModelID]; ok && view.BlockedReason == "" && now.Sub(moved) < cooldown {
			view.BlockedReason = "This model was moved automatically a short time ago"
		}
		plan.Moves = append(plan.Moves, view)
	}
	plan.Auto = GPUPlacementAutoView{Enabled: config.PlacementMode == modelmanager.PlacementAuto, LastOutcome: service.auto.lastOutcome, LastMoveModel: service.auto.lastMovedModel}
	if !service.auto.lastRunAt.IsZero() {
		at := service.auto.lastRunAt.UTC()
		plan.Auto.LastRunAt = &at
	}
	if !service.auto.lastMovedModelT.IsZero() {
		at := service.auto.lastMovedModelT.UTC()
		plan.Auto.LastMoveAt = &at
	}
	// A pending move ends when its model is served by another container, or
	// after the cooldown if the deployment failed and left it in place.
	if service.auto.pendingModel != "" {
		if view, present := views[service.auto.pendingModel]; (present && view.ContainerID != service.auto.pendingSource) || now.Sub(service.auto.pendingAt) >= cooldown {
			service.auto.pendingModel = ""
		}
	}
	switch {
	case !plan.Auto.Enabled:
		plan.Auto.BlockedReason = "Automatic placement is not enabled (MODEL_GPU_PLACEMENT=" + config.PlacementMode + ")"
	case len(plan.Moves) == 0:
		plan.Auto.BlockedReason = "The current placement cannot be improved"
	case !plan.Improves:
		plan.Auto.BlockedReason = "The plan does not let more models fit"
	case ledger.QueueDepth > 0:
		plan.Auto.BlockedReason = "Requests are waiting for admission"
	case service.auto.pendingModel != "":
		plan.Auto.BlockedReason = "The previous automatic move is still being deployed"
	default:
		for _, state := range ledger.Models {
			if state.Draining || state.Preparing {
				plan.Auto.BlockedReason = "A model deployment is in progress"
			}
		}
	}
	return plan, nil
}

// moveBlockedReason applies the automatic loop's safety rules to one model: it
// must be idle, and either unloaded or evictable under the manager's own
// minimum residency. An administrator may still move it explicitly.
func moveBlockedReason(models map[string]modelmanager.ModelSnapshot, containerID string, minResidency time.Duration, now time.Time) string {
	state, known := models[containerID]
	switch {
	case !known:
		return "The model is not in the manager inventory"
	case state.Blocked || state.Draining || state.Preparing:
		return "A deployment of this model is in progress"
	case state.Active || state.QueueDepth > 0:
		return "The model is serving or has waiting requests"
	case state.State == dockerAPI.RuntimeUnloaded && state.ReservedMiB == 0:
		return ""
	case state.State == dockerAPI.RuntimeReady && state.Loaded:
		if now.Sub(state.LastUsedAt) < minResidency {
			return "The model was used too recently to be evicted"
		}
		return ""
	default:
		return fmt.Sprintf("The model is in state %s, which is not safe to move automatically", state.State)
	}
}

// RunAutoPlacement applies at most one planned move per interval while
// MODEL_GPU_PLACEMENT=auto. It returns at once in every other mode.
func (service *GPUAdministrationService) RunAutoPlacement(ctx context.Context) {
	if service.Manager == nil {
		return
	}
	config := service.Manager.Settings()
	if config.PlacementMode != modelmanager.PlacementAuto || config.PlacementInterval <= 0 {
		return
	}
	log.Printf("[gpu-placement] automatic placement enabled interval=%s", config.PlacementInterval)
	ticker := time.NewTicker(config.PlacementInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycleCtx, cancel := context.WithTimeout(ctx, config.PlacementInterval)
			service.autoPlacementCycle(cycleCtx)
			cancel()
		}
	}
}

// autoPlacementCycle plans once and applies the first move that is safe now.
// Every refusal is recorded and shown by the plan endpoint instead of forced.
func (service *GPUAdministrationService) autoPlacementCycle(ctx context.Context) string {
	config := service.Manager.Settings()
	if config.PlacementMode != modelmanager.PlacementAuto {
		return ""
	}
	record := func(outcome string) string {
		service.autoMu.Lock()
		changed := service.auto.lastOutcome != outcome
		service.auto.lastRunAt, service.auto.lastOutcome = service.now(), outcome
		service.autoMu.Unlock()
		if changed {
			log.Printf("[gpu-placement] %s", outcome)
		}
		return outcome
	}
	plan, err := service.PlacementPlan(ctx, config.PlacementTenantID)
	if err != nil {
		return record("skipped: the plan could not be computed: " + err.Error())
	}
	if plan.Auto.BlockedReason != "" {
		return record("skipped: " + plan.Auto.BlockedReason)
	}
	blocked := ""
	for _, move := range plan.Moves {
		if move.BlockedReason != "" {
			blocked += fmt.Sprintf(" model %s: %s.", move.ModelID, move.BlockedReason)
			continue
		}
		reason := fmt.Sprintf("%d of %d models fit now, %d after the plan (%s reservations, %d MiB for this model)", plan.ModelsFitBefore, plan.ModelsTotal, plan.ModelsFitAfter, plan.ReservationSource, move.ReservationMiB)
		log.Printf("[gpu-placement] moving model=%s container=%.12s from=%s to=%s reason=%q", move.ModelID, move.ContainerID, move.FromGPUUUID, move.ToGPUUUID, reason)
		result, moveErr := service.Move(ctx, config.PlacementTenantID, config.PlacementActor, move.ModelID, move.ToGPUUUID, move.ContainerID)
		if moveErr != nil {
			service.Manager.ObservePlacementMove(false)
			return record(fmt.Sprintf("skipped: move of model %s to %s was refused: %v", move.ModelID, move.ToGPUUUID, moveErr))
		}
		service.Manager.ObservePlacementMove(true)
		at := service.now()
		service.autoMu.Lock()
		if service.auto.lastMoveAt == nil {
			service.auto.lastMoveAt = map[string]time.Time{}
		}
		service.auto.lastMoveAt[move.ModelID] = at
		service.auto.pendingModel, service.auto.pendingSource, service.auto.pendingAt = move.ModelID, move.ContainerID, at
		service.auto.lastMovedModel, service.auto.lastMovedModelT = move.ModelID, at
		service.autoMu.Unlock()
		return record(fmt.Sprintf("moved model %s from %s to %s (deployment %s): %s", move.ModelID, move.FromGPUUUID, move.ToGPUUUID, result.Operation.ID, reason))
	}
	return record("skipped: no planned move is safe now." + blocked)
}
