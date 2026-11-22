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
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelmanager"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// ErrGPUPlacementPinned is returned while MODEL_GPU_PLACEMENT=pinned.
var ErrGPUPlacementPinned = errors.New("GPU placement planning is disabled")

// maxAutoMoveFailures is how many failed automatic moves of one model are
// tolerated before it is left alone until a restart or a manual move.
const maxAutoMoveFailures = 3

const reasonNoGPU = "This model does not require a GPU"
const reasonStopped = "The container is stopped and not pinned to a GPU"

// GPUAdminOperations reads the state of a deployment started by a move.
type GPUAdminOperations interface {
	Get(ctx context.Context, tenantID, modelID, operationID string) (entity.InferenceModelDeploymentOperation, error)
}

type GPUPlacementMoveView struct {
	ModelID        string `json:"modelId"`
	ContainerID    string `json:"containerId"`
	FromGPUUUID    string `json:"fromGpuUuid"`
	ToGPUUUID      string `json:"toGpuUuid"`
	ReservationMiB int    `json:"reservationMiB"`
	// ModelsFitAlone is how many models fit if only this move is applied.
	ModelsFitAlone int `json:"modelsFitAlone"`
	// BlockedReason says why the automatic loop would not apply this move now.
	BlockedReason string `json:"blockedReason,omitempty"`
	// A move keeps the image the container runs; these fields show when that
	// is not the image registered in the catalog.
	GPUModelImageView
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
	GPUModelImageView
}
type GPUPlacementSkippedView struct {
	ModelID string `json:"modelId"`
	Reason  string `json:"reason"`
}

// GPUPlacementFailureView reports a model whose automatic moves failed.
type GPUPlacementFailureView struct {
	ModelID    string     `json:"modelId"`
	Failures   int        `json:"failures"`
	RetryAfter *time.Time `json:"retryAfter"`
	// Excluded models are not moved automatically again until the gateway
	// restarts or an administrator moves them.
	Excluded  bool   `json:"excluded"`
	LastError string `json:"lastError,omitempty"`
}
type GPUPlacementAutoView struct {
	Enabled bool `json:"enabled"`
	// BlockedReason says why the automatic loop would apply nothing now.
	BlockedReason string                    `json:"blockedReason,omitempty"`
	Window        string                    `json:"window,omitempty"`
	LastRunAt     *time.Time                `json:"lastRunAt"`
	LastOutcome   string                    `json:"lastOutcome,omitempty"`
	LastMoveAt    *time.Time                `json:"lastMoveAt"`
	LastMoveModel string                    `json:"lastMoveModelId,omitempty"`
	Failures      []GPUPlacementFailureView `json:"failures"`
}
type GPUPlacementPlan struct {
	SampledAt         time.Time `json:"sampledAt"`
	Mode              string    `json:"mode"`
	ReservationSource string    `json:"reservationSource"`
	Strategy          string    `json:"strategy"`
	// Incomplete is true when some model's GPU reservation could not be
	// determined: the plan is then optimistic and is never applied automatically.
	Incomplete        bool     `json:"incomplete"`
	IncompleteReasons []string `json:"incompleteReasons"`
	// ImageDrift is true when at least one model runs an image other than its
	// registered catalog version; ImageDriftModels names them. Moves keep the
	// running image either way.
	ImageDrift       bool                      `json:"imageDrift"`
	ImageDriftModels []string                  `json:"imageDriftModels"`
	Current          map[string]string         `json:"current"`
	Proposed         map[string]string         `json:"proposed"`
	Moves            []GPUPlacementMoveView    `json:"moves"`
	Devices          []GPUPlacementDeviceView  `json:"devices"`
	Models           []GPUPlacementModelView   `json:"models"`
	Skipped          []GPUPlacementSkippedView `json:"skipped"`
	ModelsTotal      int                       `json:"modelsTotal"`
	ModelsFitBefore  int                       `json:"modelsFitBefore"`
	ModelsFitAfter   int                       `json:"modelsFitAfter"`
	Improves         bool                      `json:"improves"`
	Auto             GPUPlacementAutoView      `json:"auto"`
}

type autoModelState struct {
	failures   int
	retryAfter time.Time
	excluded   bool
	lastError  string
	movedAt    time.Time // last accepted automatic move
}

type autoPendingMove struct {
	tenantID, modelID string
	source            string // the container the model was moved away from
	operationID       string
	at                time.Time
}

type autoPlacementState struct {
	lastRunAt      time.Time
	lastOutcome    string
	pending        *autoPendingMove
	models         map[string]*autoModelState
	lastMovedModel string
	lastMovedAt    time.Time
}

func (state *autoPlacementState) model(id string) *autoModelState {
	if state.models == nil {
		state.models = map[string]*autoModelState{}
	}
	if state.models[id] == nil {
		state.models[id] = &autoModelState{}
	}
	return state.models[id]
}

// fail records one failed automatic move: the next attempt waits twice as long
// each time, and after maxAutoMoveFailures the model is excluded.
func (state *autoPlacementState) fail(id, reason string, now time.Time, cooldown time.Duration) {
	model := state.model(id)
	model.failures++
	model.lastError = reason
	model.retryAfter = now.Add(cooldown << min(model.failures-1, 10))
	model.excluded = model.failures >= maxAutoMoveFailures
}

func (service *GPUAdministrationService) now() time.Time {
	if service.Clock != nil {
		return service.Clock()
	}
	return time.Now()
}

// autoMoveCooldown keeps a model from being redeployed again soon after an
// automatic move, bounds how long an unfinished move blocks the next one, and
// is the first delay after a failed move.
func autoMoveCooldown(interval time.Duration) time.Duration {
	return max(6*interval, 30*time.Minute)
}

// settlePendingMove decides what became of the last automatic move. The
// deployment runs asynchronously: it succeeded when the model is served by
// another container, failed when its operation says so or when nothing changed
// for a whole cooldown, and is otherwise still in progress.
func (service *GPUAdministrationService) settlePendingMove(ctx context.Context, containers map[string]string, tenantID string, cooldown time.Duration, now time.Time) {
	service.autoMu.Lock()
	pending := service.auto.pending
	service.autoMu.Unlock()
	if pending == nil {
		return
	}
	outcome, reason := "", ""
	current, visible := containers[pending.modelID]
	switch {
	case visible && tenantID == pending.tenantID && current != pending.source:
		outcome = "succeeded"
	case service.Operations != nil && pending.operationID != "":
		operation, err := service.Operations.Get(ctx, pending.tenantID, pending.modelID, pending.operationID)
		if err == nil {
			switch operation.Status {
			case entity.InferenceModelDeploymentOperationFailed, entity.InferenceModelDeploymentOperationRolledBack, entity.InferenceModelDeploymentOperationInterrupted:
				outcome, reason = "failed", "deployment "+pending.operationID+" ended "+string(operation.Status)
			}
		}
	}
	if outcome == "" && now.Sub(pending.at) >= cooldown {
		outcome, reason = "failed", "deployment did not replace the container within "+cooldown.String()
	}
	if outcome == "" {
		return
	}
	service.autoMu.Lock()
	defer service.autoMu.Unlock()
	if service.auto.pending != pending {
		return
	}
	service.auto.pending = nil
	if outcome == "succeeded" {
		model := service.auto.model(pending.modelID)
		model.failures, model.retryAfter, model.lastError = 0, time.Time{}, ""
		return
	}
	service.auto.fail(pending.modelID, reason, now, cooldown)
	log.Printf("[gpu-placement] automatic move of model=%s failed: %s (failures=%d)", pending.modelID, reason, service.auto.models[pending.modelID].failures)
}

// PlacementPlan computes, without changing any model, where the workspace's
// models should run so that as many reservations as possible fit at once.
func (service *GPUAdministrationService) PlacementPlan(ctx context.Context, tenantID string) (GPUPlacementPlan, error) {
	plan := GPUPlacementPlan{ImageDriftModels: []string{}, IncompleteReasons: []string{}, Current: map[string]string{}, Proposed: map[string]string{}, Moves: []GPUPlacementMoveView{}, Devices: []GPUPlacementDeviceView{}, Models: []GPUPlacementModelView{}, Skipped: []GPUPlacementSkippedView{}}
	plan.Auto.Failures = []GPUPlacementFailureView{}
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
	incomplete := func(reason string) {
		plan.Incomplete = true
		plan.IncompleteReasons = append(plan.IncompleteReasons, reason)
	}

	allowed := map[string]bool{}
	for _, uuid := range config.AllowedGPUUUIDs {
		allowed[uuid] = true
	}
	tenantContainers := map[string]struct{}{}
	containers := map[string]string{}
	for _, model := range status.Models {
		tenantContainers[model.ContainerID] = struct{}{}
		containers[model.ModelID] = model.ContainerID
	}
	// Reservations of models registered elsewhere are fixed load on a device.
	// One whose resource contract the manager has never read (its container
	// was stopped at startup) cannot be counted, so the plan is incomplete.
	other := map[string]int{}
	unknownOthers := 0
	for id, model := range measurements.Models {
		if _, mine := tenantContainers[id]; mine {
			continue
		}
		state := ledger.Models[id]
		if state.Blocked && state.ReservedMiB == 0 && !state.Loaded {
			continue // tombstone of a replaced container
		}
		if !model.ResourcesKnown && state.ReservedMiB == 0 {
			unknownOthers++
			continue
		}
		if model.GPURequired || state.ReservedMiB > 0 {
			other[model.GPUUUID] += max(model.ReservationMiB, state.ReservedMiB)
		}
	}
	if unknownOthers > 0 {
		incomplete(fmt.Sprintf("%d model(s) outside this workspace have an unknown GPU reservation", unknownOthers))
	}
	gpus := make([]modelmanager.PlacementGPU, 0, len(status.Devices))
	for _, device := range status.Devices {
		gpus = append(gpus, modelmanager.PlacementGPU{UUID: device.UUID, CapacityMiB: max(0, device.ManagerCapacityMiB-other[device.UUID]), Allowed: len(allowed) == 0 || allowed[device.UUID]})
	}

	views := map[string]GPUPlacementModelView{}
	models := make([]modelmanager.PlacementModel, 0, len(status.Models))
	for _, model := range status.Models {
		held := ledger.Models[model.ContainerID].ReservedMiB
		reason := ""
		switch {
		case model.Stopped && model.GPUUUID == nil && held == 0:
			// A stopped container pinned to no GPU holds no GPU memory and
			// has no placement to plan around.
			reason = reasonStopped
		case model.ReservationMiB != nil && *model.ReservationMiB == 0 && held == 0:
			reason = reasonNoGPU
		case model.GPUUUID == nil:
			reason = model.Reason
			if reason == "" {
				reason = "Container has no explicit GPU placement"
			}
		case model.ReservationMiB == nil && held == 0:
			reason = "The model's GPU reservation is not known"
		default:
			if _, configured := ledger.GPUs[*model.GPUUUID]; !configured {
				reason = "The model's GPU is not managed"
			}
		}
		if reason != "" {
			plan.Skipped = append(plan.Skipped, GPUPlacementSkippedView{ModelID: model.ModelID, Reason: reason})
			// Only a model known to need no GPU is safe to leave out.
			if reason != reasonNoGPU && reason != reasonStopped {
				incomplete("Model " + model.ModelID + " is not counted: " + reason)
			}
			continue
		}
		reservation := held
		if model.ReservationMiB != nil {
			reservation = *model.ReservationMiB
		}
		views[model.ModelID] = GPUPlacementModelView{ModelID: model.ModelID, ContainerID: model.ContainerID, CurrentGPUUUID: *model.GPUUUID, ReservationMiB: reservation, Movable: model.PlacementSupported, Reason: model.Reason, GPUModelImageView: model.GPUModelImageView}
		if model.ImageDrift {
			plan.ImageDrift = true
			plan.ImageDriftModels = append(plan.ImageDriftModels, model.ModelID)
		}
		models = append(models, modelmanager.PlacementModel{ID: model.ModelID, GPUUUID: *model.GPUUUID, ReservationMiB: reservation, Movable: model.PlacementSupported})
	}
	sort.Strings(plan.IncompleteReasons)
	sort.Strings(plan.ImageDriftModels)

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

	cooldown := autoMoveCooldown(config.PlacementInterval)
	service.settlePendingMove(ctx, containers, tenantID, cooldown, now)

	service.autoMu.Lock()
	defer service.autoMu.Unlock()
	for _, move := range computed.Moves {
		view := GPUPlacementMoveView{ModelID: move.ModelID, ContainerID: views[move.ModelID].ContainerID, FromGPUUUID: move.From, ToGPUUUID: move.To, ReservationMiB: move.ReservationMiB, ModelsFitAlone: move.FitAlone}
		view.GPUModelImageView = views[move.ModelID].GPUModelImageView
		view.BlockedReason = moveBlockedReason(ledger.Models, view.ContainerID, config, now)
		state := service.auto.models[move.ModelID]
		switch {
		case view.BlockedReason != "":
		case view.RunningImageUnavailable:
			// Never move automatically when the container could not be
			// recreated from the image it runs.
			view.BlockedReason = fmt.Sprintf("The image this model runs is not present on the host; a move would deploy the registered image %s instead", view.RegisteredImage)
		case move.FitAlone < computed.FitBefore:
			// One half of a swap: alone it would leave fewer models fitting.
			view.BlockedReason = "Applying this move alone would let fewer models fit"
		case state == nil:
		case state.excluded:
			view.BlockedReason = fmt.Sprintf("Automatic moves of this model are suspended after %d failed attempts; move it manually or restart the gateway", state.failures)
		case now.Before(state.retryAfter):
			view.BlockedReason = fmt.Sprintf("An automatic move of this model failed %d time(s); next attempt after %s", state.failures, state.retryAfter.UTC().Format(time.RFC3339))
		case !state.movedAt.IsZero() && now.Sub(state.movedAt) < cooldown:
			view.BlockedReason = "This model was moved automatically a short time ago"
		}
		plan.Moves = append(plan.Moves, view)
	}
	plan.Auto.Enabled, plan.Auto.Window = config.PlacementMode == modelmanager.PlacementAuto, config.PlacementWindow
	plan.Auto.LastOutcome, plan.Auto.LastMoveModel = service.auto.lastOutcome, service.auto.lastMovedModel
	if !service.auto.lastRunAt.IsZero() {
		at := service.auto.lastRunAt.UTC()
		plan.Auto.LastRunAt = &at
	}
	if !service.auto.lastMovedAt.IsZero() {
		at := service.auto.lastMovedAt.UTC()
		plan.Auto.LastMoveAt = &at
	}
	for id, state := range service.auto.models {
		if state.failures == 0 {
			continue
		}
		failure := GPUPlacementFailureView{ModelID: id, Failures: state.failures, Excluded: state.excluded, LastError: state.lastError}
		if !state.excluded {
			retry := state.retryAfter.UTC()
			failure.RetryAfter = &retry
		}
		plan.Auto.Failures = append(plan.Auto.Failures, failure)
	}
	sort.Slice(plan.Auto.Failures, func(i, j int) bool { return plan.Auto.Failures[i].ModelID < plan.Auto.Failures[j].ModelID })
	switch {
	case !plan.Auto.Enabled:
		plan.Auto.BlockedReason = "Automatic placement is not enabled (MODEL_GPU_PLACEMENT=" + config.PlacementMode + ")"
	case plan.Incomplete:
		plan.Auto.BlockedReason = "The plan is incomplete: " + plan.IncompleteReasons[0]
	case len(plan.Moves) == 0:
		plan.Auto.BlockedReason = "The current placement cannot be improved"
	case !plan.Improves:
		plan.Auto.BlockedReason = "The plan does not let more models fit"
	case !modelmanager.InPlacementWindow(config.PlacementWindow, now):
		plan.Auto.BlockedReason = "Outside the automatic placement window " + config.PlacementWindow
	case ledger.QueueDepth > 0:
		plan.Auto.BlockedReason = "Requests are waiting for admission"
	case service.auto.pending != nil:
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

// moveBlockedReason applies the automatic loop's safety rules to one model.
// A move drains and redeploys the container, and requests for the model are
// refused meanwhile, so the model must be unloaded or have been idle for
// MODEL_GPU_PLACEMENT_MIN_IDLE_SECONDS. An administrator may still move it.
func moveBlockedReason(models map[string]modelmanager.ModelSnapshot, containerID string, config modelmanager.Config, now time.Time) string {
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
		if idle := now.Sub(state.LastUsedAt); idle < max(config.PlacementMinIdle, config.MinResidency) {
			return fmt.Sprintf("The model is loaded and was used %s ago; it must be idle for %s", idle.Truncate(time.Second), max(config.PlacementMinIdle, config.MinResidency))
		}
		return ""
	default:
		return fmt.Sprintf("The model is in state %s, which is not safe to move automatically", state.State)
	}
}

// Move redeploys a model on another GPU at an administrator's request. It
// also clears the automatic loop's failure record for that model.
func (service *GPUAdministrationService) Move(ctx context.Context, tenantID, actor, modelID, uuid, expectedContainerID string) (serviceTypes.DeployInferenceModelVersionResult, error) {
	result, err := service.move(ctx, tenantID, actor, modelID, uuid, expectedContainerID, false)
	if err == nil {
		service.autoMu.Lock()
		delete(service.auto.models, modelID)
		if service.auto.pending != nil && service.auto.pending.modelID == modelID {
			service.auto.pending = nil
		}
		service.autoMu.Unlock()
	}
	return result, err
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
	log.Printf("[gpu-placement] automatic placement enabled interval=%s window=%q", config.PlacementInterval, config.PlacementWindow)
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

// autoPlacementCycle plans once and applies the first move that is safe now
// and, applied alone, does not reduce how many models fit. The plan is computed
// again on the next cycle, so a move is never taken from an outdated plan.
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
		// The plan took a moment to compute: look at the model once more, so a
		// request that arrived meanwhile is never interrupted. The deployment
		// repeats this check atomically when it closes admission.
		checkedAt := service.now()
		if busy := moveBlockedReason(service.Manager.Snapshot().Models, move.ContainerID, config, checkedAt); busy != "" {
			blocked += fmt.Sprintf(" model %s: %s.", move.ModelID, busy)
			continue
		}
		reason := fmt.Sprintf("%d of %d models fit now, %d after this move, %d after the plan (%s reservations, %d MiB for this model)", plan.ModelsFitBefore, plan.ModelsTotal, move.ModelsFitAlone, plan.ModelsFitAfter, plan.ReservationSource, move.ReservationMiB)
		log.Printf("[gpu-placement] moving model=%s container=%.12s from=%s to=%s image=%q image_drift=%t reason=%q", move.ModelID, move.ContainerID, move.FromGPUUUID, move.ToGPUUUID, move.RunningImage, move.ImageDrift, reason)
		result, moveErr := service.move(ctx, config.PlacementTenantID, config.PlacementActor, move.ModelID, move.ToGPUUUID, move.ContainerID, true)
		at := service.now()
		cooldown := autoMoveCooldown(config.PlacementInterval)
		if moveErr != nil {
			service.Manager.ObservePlacementMove(false)
			service.autoMu.Lock()
			service.auto.fail(move.ModelID, "move refused: "+moveErr.Error(), at, cooldown)
			failures := service.auto.models[move.ModelID].failures
			service.autoMu.Unlock()
			return record(fmt.Sprintf("skipped: move of model %s to %s was refused (failure %d of %d): %v", move.ModelID, move.ToGPUUUID, failures, maxAutoMoveFailures, moveErr))
		}
		service.Manager.ObservePlacementMove(true)
		service.autoMu.Lock()
		service.auto.model(move.ModelID).movedAt = at
		service.auto.pending = &autoPendingMove{tenantID: config.PlacementTenantID, modelID: move.ModelID, source: move.ContainerID, operationID: result.Operation.ID, at: at}
		service.auto.lastMovedModel, service.auto.lastMovedAt = move.ModelID, at
		service.autoMu.Unlock()
		return record(fmt.Sprintf("moved model %s from %s to %s (deployment %s): %s", move.ModelID, move.FromGPUUUID, move.ToGPUUUID, result.Operation.ID, reason))
	}
	return record("skipped: no planned move is safe now." + blocked)
}
