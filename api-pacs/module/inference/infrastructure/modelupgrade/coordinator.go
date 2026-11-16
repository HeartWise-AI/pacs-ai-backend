package modelupgrade

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelmanager"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

type UpgradeStage = entity.InferenceModelDeploymentOperationStage

const (
	UpgradeStageResolveActive  = entity.InferenceModelDeploymentStageResolveActive
	UpgradeStageResolveVersion = entity.InferenceModelDeploymentStageResolveVersion
	UpgradeStagePull           = entity.InferenceModelDeploymentStagePull
	UpgradeStageCreate         = entity.InferenceModelDeploymentStageCreate
	UpgradeStageValidate       = entity.InferenceModelDeploymentStageValidate
	UpgradeStageDrain          = entity.InferenceModelDeploymentStageDrain
	UpgradeStagePrepare        = entity.InferenceModelDeploymentStagePrepare
	UpgradeStageActivate       = entity.InferenceModelDeploymentStageActivate
)

// UpgradeError identifies the failed stage and reports rollback separately.
type UpgradeError struct {
	Stage             UpgradeStage
	Err               error
	RollbackAttempted bool
	RollbackErr       error
}

func (err *UpgradeError) Error() string {
	if err.RollbackErr != nil {
		return fmt.Sprintf("model upgrade failed at %s: %v; rollback: %v", err.Stage, err.Err, err.RollbackErr)
	}
	return fmt.Sprintf("model upgrade failed at %s: %v", err.Stage, err.Err)
}

func (err *UpgradeError) Unwrap() error { return err.Err }

type UpgradeRequest struct {
	TargetGPUUUID       string
	ExpectedContainerID string
	TenantID            string
	ModelID             string
	VersionID           string
	// KeepRunningImage makes this a move: the candidate is recreated from the
	// image and configuration of the running container instead of the catalog
	// version, which is then only the reference the running image is compared
	// with. If the running image is no longer present locally the catalog
	// version is deployed as before, unless Automatic is set.
	KeepRunningImage bool
	// Automatic marks a move started by the placement loop rather than by an
	// administrator. It is refused when the model is serving or has waiting
	// requests, and when it could not keep the running image.
	Automatic bool
	OnStage   func(context.Context, UpgradeStage) error
}

type UpgradeResult struct {
	Previous      entity.InferenceModelDeployment
	Active        entity.InferenceModelDeployment
	ContainerName string
	// Image reports, for a move, which image the new container was created from.
	Image *RunningImage
	// ImageSource is "running" when a move kept the running container's image
	// and "registered" when the catalog version was deployed.
	ImageSource string
}

const (
	ImageSourceRunning    = "running"
	ImageSourceRegistered = "registered"
)

// ErrModelBusy refuses an automatic move of a model that is in use.
var ErrModelBusy = modelmanager.ErrModelBusy

// ErrMoveWouldChangeImage refuses an automatic move that could not recreate
// the container from the image it runs.
var ErrMoveWouldChangeImage = errors.New("moving the model would change its image")

// idleDrainer is implemented by a manager that can refuse to drain a model
// that has an admitted or waiting request.
type idleDrainer interface {
	BlockAndDrainIdle(context.Context, string) error
}

type CoordinatorConfig struct {
	DrainTimeout              time.Duration
	RollbackTimeout           time.Duration
	AllowCandidateDeployments bool
	// NameHandOverTimeout bounds the name hand-over that ends a move,
	// including the wait for the model's admitted request to finish.
	NameHandOverTimeout time.Duration
	// NameVerifyTimeout bounds the wait for the moved model to answer under
	// its name before the renames are undone.
	NameVerifyTimeout time.Duration
}

type UpgradeDocker interface {
	docker.CandidateDockerSDKInterface
}

type UpgradeAPI interface {
	CandidateAPI
}

type UpgradeValidator interface {
	ValidateImageReference(string, string) (string, error)
	ResolveImage(context.Context, string, string) (ResolvedCandidateImage, error)
	ValidateContainer(context.Context, ValidateCandidateContainer) (ValidatedCandidate, error)
}

type UpgradeManager interface {
	BlockAndDrain(context.Context, string) error
	ReplaceDrained(string, string) error
	PrepareBlocked(context.Context, string) error
	Unblock(string) error
}

type UpgradeModelReader interface {
	SelectInferenceModelByID(context.Context, string) (entity.InferenceModel, error)
}

type UpgradeVersionReader interface {
	SelectInferenceModelVersion(context.Context, string) (entity.InferenceModelVersion, error)
}

type UpgradeRegistrationRepository interface {
	ReplaceInferenceModelRegistration(
		context.Context,
		repositoryTypes.ReplaceInferenceModelRegistration,
	) (entity.InferenceModelDeployment, error)
	RestoreInferenceModelRegistration(context.Context, repositoryTypes.RestoreInferenceModelRegistration) error
}

type UpgradeCoordinator struct {
	docker        UpgradeDocker
	api           UpgradeAPI
	validator     UpgradeValidator
	manager       UpgradeManager
	models        UpgradeModelReader
	versions      UpgradeVersionReader
	registrations UpgradeRegistrationRepository
	config        CoordinatorConfig
	now           func() time.Time
	inFlightMu    sync.Mutex
	inFlight      map[string]struct{}
}

func NewUpgradeCoordinator(
	dockerClient UpgradeDocker,
	apiClient UpgradeAPI,
	validator UpgradeValidator,
	manager UpgradeManager,
	models UpgradeModelReader,
	versions UpgradeVersionReader,
	registrations UpgradeRegistrationRepository,
	config CoordinatorConfig,
) (*UpgradeCoordinator, error) {
	if dockerClient == nil || apiClient == nil || validator == nil || manager == nil || models == nil || versions == nil || registrations == nil {
		return nil, errors.New("model upgrade coordinator dependencies are required")
	}
	if config.DrainTimeout <= 0 {
		config.DrainTimeout = 5 * time.Minute
	}
	if config.RollbackTimeout <= 0 {
		config.RollbackTimeout = 5 * time.Minute
	}
	if config.NameHandOverTimeout <= 0 {
		config.NameHandOverTimeout = 2 * time.Minute
	}
	if config.NameVerifyTimeout <= 0 {
		config.NameVerifyTimeout = 10 * time.Second
	}
	return &UpgradeCoordinator{
		docker: dockerClient, api: apiClient, validator: validator, manager: manager,
		models: models, versions: versions, registrations: registrations, config: config, now: time.Now,
		inFlight: make(map[string]struct{}),
	}, nil
}

// Upgrade stages and validates a candidate before closing active admission. It
// does not remove the old container after success; that rollback baseline is
// intentionally retained for a later cleanup step. A move (KeepRunningImage)
// ends by stopping the old container and handing its name to the new one; see
// handOverContainerNames.
func (coordinator *UpgradeCoordinator) Upgrade(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ModelID = strings.TrimSpace(request.ModelID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	if request.TenantID == "" || request.ModelID == "" || request.VersionID == "" {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New(apiError.InvalidPayload), nil)
	}
	// Staging a candidate can start its process before drain. Do not let an
	// unaccounted startup inventory create another potential GPU consumer.
	if manager, ok := coordinator.manager.(interface {
		Snapshot() modelmanager.Snapshot
	}); ok && !manager.Snapshot().Ready {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, modelmanager.ErrNotReconciled, nil)
	}
	upgradeKey := request.TenantID + "\x00" + request.ModelID
	if !coordinator.beginUpgrade(upgradeKey) {
		return UpgradeResult{}, upgradeFailure(
			UpgradeStageResolveActive, errors.New(apiError.InferenceUpgradeConflict), nil,
		)
	}
	defer coordinator.endUpgrade(upgradeKey)
	if err := reportUpgradeStage(ctx, request, UpgradeStageResolveActive); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, err, nil)
	}

	registered, err := coordinator.models.SelectInferenceModelByID(ctx, request.ModelID)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, err, nil)
	}
	if registered.TenantID != request.TenantID {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New(apiError.MissingRecord), nil)
	}
	if request.ExpectedContainerID != "" && registered.ContainerID != request.ExpectedContainerID {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New(apiError.InferenceUpgradeConflict), nil)
	}
	registered.ContainerID = strings.TrimSpace(registered.ContainerID)
	registered.DockerImage = strings.TrimSpace(registered.DockerImage)
	if registered.ContainerID == "" || registered.DockerImage == "" {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New("active registration is incomplete"), nil)
	}
	activeContainer, err := coordinator.docker.GetContainerInfo(ctx, registered.ContainerID)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, err, nil)
	}
	activeName := strings.TrimPrefix(strings.TrimSpace(activeContainer.Name), "/")
	if activeContainer.ID != registered.ContainerID || activeName == "" {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New("active container identity does not match registration"), nil)
	}
	activeModelInfo, err := coordinator.api.GetModelInfo(ctx, activeName)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, err, nil)
	}
	targetGPUUUID := strings.TrimSpace(request.TargetGPUUUID)
	if targetGPUUUID == "" {
		targetGPUUUID = strings.TrimSpace(registered.PreferredGPUUUID)
	}
	if targetGPUUUID != "" {
		var validate func(string, api.ModelResources) error
		if strings.TrimSpace(request.TargetGPUUUID) == "" {
			if preference, ok := coordinator.manager.(interface {
				ValidateGPUPreference(string, api.ModelResources) error
			}); ok {
				validate = preference.ValidateGPUPreference
			}
		}
		if placement, ok := coordinator.manager.(interface {
			ValidateGPUPlacement(string, api.ModelResources) error
		}); ok && validate == nil {
			validate = placement.ValidateGPUPlacement
		}
		if validate == nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New(apiError.InferenceUpgradeUnavailable), nil)
		}
		if err := validate(targetGPUUUID, activeModelInfo.Data.Resources); err != nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, err, nil)
		}
	}
	if err = reportUpgradeStage(ctx, request, UpgradeStageResolveVersion); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveVersion, err, nil)
	}
	version, err := coordinator.versions.SelectInferenceModelVersion(ctx, request.VersionID)
	var running *RunningImage
	if request.KeepRunningImage {
		// A move does not deploy the catalog version; a registration whose
		// catalog entry is unreadable can still be moved on its own image.
		var catalog *entity.InferenceModelVersion
		if err == nil {
			version.Normalize()
			catalog = &version
		}
		resolved := ResolveRunningImage(ctx, coordinator.docker, activeContainer, registered, catalog)
		switch {
		case resolved.Available:
			running = &resolved
			log.Printf(
				"[model-deployment] event=move_image model_id=%s container=%.12s image_source=running running_image=%q running_image_id=%s registered_image=%q image_drift=%t",
				request.ModelID, registered.ContainerID, resolved.Reference, resolved.ImageID, resolved.RegisteredReference, resolved.Drift,
			)
		case request.Automatic:
			log.Printf(
				"[model-deployment] event=move_refused model_id=%s container=%.12s reason=running_image_unavailable running_image_id=%s registered_image=%q",
				request.ModelID, registered.ContainerID, resolved.ImageID, resolved.RegisteredReference,
			)
			return UpgradeResult{}, upgradeFailure(UpgradeStageResolveVersion, ErrMoveWouldChangeImage, nil)
		default:
			log.Printf(
				"[model-deployment] event=move_image model_id=%s container=%.12s image_source=registered reason=running_image_unavailable running_image_id=%s registered_image=%q",
				request.ModelID, registered.ContainerID, resolved.ImageID, resolved.RegisteredReference,
			)
		}
	}
	if running == nil {
		if err != nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStageResolveVersion, err, nil)
		}
		version.Normalize()
		if err = version.Validate(); err != nil || version.ID != request.VersionID ||
			version.Status != entity.InferenceModelVersionAvailable || version.ModelID != activeModelInfo.Data.ModelID {
			return UpgradeResult{}, upgradeFailure(
				UpgradeStageResolveVersion, errors.New(apiError.InvalidPayload), nil,
			)
		}
		if version.Channel == entity.InferenceModelVersionCandidate && !coordinator.config.AllowCandidateDeployments {
			return UpgradeResult{}, upgradeFailure(
				UpgradeStageResolveVersion, errors.New(apiError.InferenceCandidateDeploymentDisabled), nil,
			)
		}
	}

	if err = reportUpgradeStage(ctx, request, UpgradeStagePull); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStagePull, err, nil)
	}
	var resolvedImage ResolvedCandidateImage
	if running != nil {
		// Nothing is pulled: the image is the one already running here.
		resolvedImage = ResolvedCandidateImage{
			Reference: running.Reference, Digest: running.Digest, LocalImageID: running.ImageID,
		}
	} else {
		normalizedImage, referenceErr := coordinator.validator.ValidateImageReference(version.ImageReference, version.ImageDigest)
		if referenceErr != nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStagePull, referenceErr, nil)
		}
		if err = coordinator.docker.PullImage(ctx, normalizedImage); err != nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStagePull, err, nil)
		}
		resolvedImage, err = coordinator.validator.ResolveImage(ctx, normalizedImage, version.ImageDigest)
		if err != nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStagePull, err, nil)
		}
	}
	if err = reportUpgradeStage(ctx, request, UpgradeStageCreate); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageCreate, err, nil)
	}
	candidateName, err := newCandidateContainerName(activeName)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageCreate, err, nil)
	}
	create := docker.CreateCandidateContainer{
		GPUUUID: targetGPUUUID, Name: candidateName, Image: resolvedImage.LocalImageID,
		Envs: append([]string(nil), registered.Envs...), ActiveContainerID: registered.ContainerID,
	}
	if running != nil {
		create.Image, create.Envs = running.CreateReference, nil
		create.KeepActiveConfiguration, create.ExpectedImageID = true, running.ImageID
	}
	candidateID, err := coordinator.docker.CreateCandidateContainer(ctx, create)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageCreate, err, nil)
	}
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return UpgradeResult{}, upgradeFailure(
			UpgradeStageCreate, errors.New("Docker returned an empty candidate container ID"),
			coordinator.removeCandidate(ctx, candidateID),
		)
	}

	if err = reportUpgradeStage(ctx, request, UpgradeStageValidate); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, err, coordinator.removeCandidate(ctx, candidateID))
	}
	validated, err := coordinator.validator.ValidateContainer(ctx, ValidateCandidateContainer{
		ContainerID: candidateID, Image: resolvedImage, ActiveModelInfo: activeModelInfo.Data,
		RegisteredOutputMode: registered.OutputMode, SameImageAsActive: running != nil,
	})
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, err, coordinator.removeCandidate(ctx, candidateID))
	}
	if targetGPUUUID != "" && validated.GPUUUID != targetGPUUUID {
		return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, errors.New("candidate GPU placement does not match requested device"), coordinator.removeCandidate(ctx, candidateID))
	}
	var deployment entity.InferenceModelDeployment
	if running != nil {
		// The whole point of a move: the new container must run the image the
		// old one runs. Checked on the created container, not on the request.
		created, inspectErr := coordinator.docker.GetContainerInfo(ctx, candidateID)
		if inspectErr != nil || created.ImageID != running.ImageID || validated.LocalImageID != running.ImageID {
			log.Printf(
				"[model-deployment] event=move_refused model_id=%s container=%.12s reason=image_changed running_image_id=%s candidate_image_id=%s",
				request.ModelID, registered.ContainerID, running.ImageID, created.ImageID,
			)
			return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, ErrMoveWouldChangeImage, coordinator.removeCandidate(ctx, candidateID))
		}
		deployment, err = DeploymentFromMovedCandidate(validated, registered, *running, request.VersionID, coordinator.now())
	} else {
		if err = validateCatalogCandidate(version, validated); err != nil {
			return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, err, coordinator.removeCandidate(ctx, candidateID))
		}
		deployment, err = DeploymentFromCandidate(validated, version.ID, coordinator.now())
	}
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, err, coordinator.removeCandidate(ctx, candidateID))
	}

	if err = reportUpgradeStage(ctx, request, UpgradeStageDrain); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageDrain, err, coordinator.removeCandidate(ctx, candidateID))
	}
	drainCtx, cancelDrain := context.WithTimeout(ctx, coordinator.config.DrainTimeout)
	if request.Automatic {
		// An automatic move never waits for, or closes admission on, a model
		// that is in use: the check and the gate are one atomic step.
		if drainer, ok := coordinator.manager.(idleDrainer); ok {
			err = drainer.BlockAndDrainIdle(drainCtx, registered.ContainerID)
		} else {
			err = errors.New(apiError.InferenceUpgradeUnavailable)
		}
		if err != nil && (errors.Is(err, ErrModelBusy) || err.Error() == apiError.InferenceUpgradeUnavailable) {
			cancelDrain()
			log.Printf("[model-deployment] event=move_refused model_id=%s container=%.12s reason=%q", request.ModelID, registered.ContainerID, err.Error())
			// Admission was never closed, so there is nothing to reopen.
			return UpgradeResult{}, upgradeRollbackFailure(UpgradeStageDrain, err, coordinator.removeCandidate(ctx, candidateID))
		}
	} else {
		err = coordinator.manager.BlockAndDrain(drainCtx, registered.ContainerID)
	}
	cancelDrain()
	if err != nil {
		rollbackErr := errors.Join(coordinator.manager.Unblock(registered.ContainerID), coordinator.removeCandidate(ctx, candidateID))
		return UpgradeResult{}, upgradeRollbackFailure(UpgradeStageDrain, err, rollbackErr)
	}
	if err = reportUpgradeStage(ctx, request, UpgradeStagePrepare); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, false)
		return UpgradeResult{}, upgradeRollbackFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.manager.ReplaceDrained(registered.ContainerID, candidateID); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, false)
		return UpgradeResult{}, upgradeRollbackFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.manager.PrepareBlocked(ctx, candidateID); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeRollbackFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.verifyPreparedCandidate(ctx, validated); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeRollbackFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}

	if err = reportUpgradeStage(ctx, request, UpgradeStageActivate); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeRollbackFailure(
			UpgradeStageActivate, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	previous, err := coordinator.registrations.ReplaceInferenceModelRegistration(ctx, repositoryTypes.ReplaceInferenceModelRegistration{
		ID: request.ModelID, TenantID: request.TenantID,
		ExpectedContainerID: registered.ContainerID, ExpectedImageReference: registered.DockerImage,
		Deployment: deployment, KeepsRunningImage: running != nil,
	})
	if err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeRollbackFailure(
			UpgradeStageActivate, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.manager.Unblock(candidateID); err != nil {
		rollbackErr := coordinator.rollbackActivated(ctx, request, registered, deployment, previous, candidateID)
		return UpgradeResult{}, upgradeRollbackFailure(UpgradeStageActivate, err, rollbackErr)
	}

	result := UpgradeResult{
		Previous: previous, Active: deployment, ContainerName: validated.ContainerName, ImageSource: ImageSourceRegistered,
	}
	if running != nil {
		result.Image, result.ImageSource = running, ImageSourceRunning
		log.Printf(
			"[model-deployment] event=moved model_id=%s from_container=%.12s to_container=%.12s gpu=%s image_source=running image=%q image_id=%s registered_image=%q image_drift=%t",
			request.ModelID, registered.ContainerID, candidateID, deployment.GPUUUID, running.Reference, running.ImageID, running.RegisteredReference, running.Drift,
		)
	} else if request.KeepRunningImage {
		log.Printf(
			"[model-deployment] event=moved model_id=%s from_container=%.12s to_container=%.12s gpu=%s image_source=registered image=%q",
			request.ModelID, registered.ContainerID, candidateID, deployment.GPUUUID, deployment.ImageReference,
		)
	}
	if request.KeepRunningImage {
		// The move is active and can no longer roll back. Whatever happens to
		// the names from here on is logged and never fails it.
		handOver := coordinator.handOverContainerNames(ctx, nameHandOver{
			Name: activeName, ModelID: request.ModelID, OldID: registered.ContainerID, NewID: candidateID,
		})
		if handOver.NewName != "" {
			result.ContainerName = handOver.NewName
		}
	}
	return result, nil
}

func reportUpgradeStage(ctx context.Context, request UpgradeRequest, stage UpgradeStage) error {
	if request.OnStage == nil {
		return nil
	}
	return request.OnStage(ctx, stage)
}

func validateCatalogCandidate(version entity.InferenceModelVersion, candidate ValidatedCandidate) error {
	if candidate.ModelInfo.ModelID != version.ModelID || candidate.ModelInfo.Version != version.Version ||
		candidate.ImageReference != version.ImageReference || candidate.ImageDigest != version.ImageDigest {
		return fmt.Errorf("%w: candidate does not match the selected catalog version", ErrCandidateContract)
	}
	if candidate.ModelInfo.Provenance == nil || version.Provenance == nil {
		return fmt.Errorf("%w: catalog provenance is incomplete", ErrCandidateContract)
	}
	candidateProvenance := candidate.ModelInfo.Provenance
	catalogProvenance := version.Provenance
	if candidateProvenance.SourceRevision == nil || catalogProvenance.SourceRevision == nil ||
		candidateProvenance.SourceRepository != catalogProvenance.SourceRepository ||
		*candidateProvenance.SourceRevision != *catalogProvenance.SourceRevision ||
		candidateProvenance.ModelRepository != catalogProvenance.ModelRepository ||
		candidateProvenance.ModelRevision != catalogProvenance.ModelRevision ||
		candidateProvenance.WeightsPath != catalogProvenance.WeightsPath ||
		candidateProvenance.WeightsSHA256 != catalogProvenance.WeightsSHA256 {
		return fmt.Errorf("%w: candidate provenance does not match the selected catalog version", ErrCandidateContract)
	}
	return nil
}

func (coordinator *UpgradeCoordinator) beginUpgrade(key string) bool {
	coordinator.inFlightMu.Lock()
	defer coordinator.inFlightMu.Unlock()
	if _, exists := coordinator.inFlight[key]; exists {
		return false
	}
	coordinator.inFlight[key] = struct{}{}
	return true
}

func (coordinator *UpgradeCoordinator) endUpgrade(key string) {
	coordinator.inFlightMu.Lock()
	delete(coordinator.inFlight, key)
	coordinator.inFlightMu.Unlock()
}

// cleanupAfterRollback removes a candidate only after the manager has
// confirmed that the old deployment is restored. Otherwise the container is
// deliberately retained as evidence and for explicit recovery.
func (coordinator *UpgradeCoordinator) cleanupAfterRollback(
	ctx context.Context,
	candidateID string,
	rollbackErr error,
) error {
	if rollbackErr != nil {
		return rollbackErr
	}
	return coordinator.removeCandidate(ctx, candidateID)
}

func (coordinator *UpgradeCoordinator) verifyPreparedCandidate(ctx context.Context, candidate ValidatedCandidate) error {
	runtime, err := coordinator.api.GetModelRuntime(ctx, candidate.ContainerName)
	if err != nil {
		return err
	}
	if err = runtime.Validate(); err != nil {
		return err
	}
	if runtime.State != api.RuntimeReady || !runtime.Loaded || runtime.ActiveRequests != 0 {
		return errors.New("prepared candidate is not idle and READY")
	}
	return nil
}

func (coordinator *UpgradeCoordinator) rollbackBeforeActivation(
	ctx context.Context,
	oldContainerID, candidateID string,
	candidateTracked bool,
) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.config.RollbackTimeout)
	defer cancel()
	if candidateTracked {
		if err := coordinator.manager.BlockAndDrain(rollbackCtx, candidateID); err != nil {
			return err
		}
		if err := coordinator.manager.ReplaceDrained(candidateID, oldContainerID); err != nil {
			return err
		}
	}
	if err := coordinator.manager.PrepareBlocked(rollbackCtx, oldContainerID); err != nil {
		return err
	}
	return coordinator.manager.Unblock(oldContainerID)
}

func (coordinator *UpgradeCoordinator) rollbackActivated(
	ctx context.Context,
	request UpgradeRequest,
	registered entity.InferenceModel,
	candidate, previous entity.InferenceModelDeployment,
	candidateID string,
) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.config.RollbackTimeout)
	defer cancel()
	if err := coordinator.manager.BlockAndDrain(rollbackCtx, candidateID); err != nil {
		return err
	}
	if err := coordinator.manager.ReplaceDrained(candidateID, registered.ContainerID); err != nil {
		return err
	}
	if err := coordinator.manager.PrepareBlocked(rollbackCtx, registered.ContainerID); err != nil {
		return err
	}
	if err := coordinator.registrations.RestoreInferenceModelRegistration(rollbackCtx, repositoryTypes.RestoreInferenceModelRegistration{
		ID: request.ModelID, TenantID: request.TenantID,
		ExpectedContainerID: candidate.ContainerID, ExpectedImageReference: candidate.ImageReference,
		Previous: previous,
	}); err != nil {
		return err
	}
	if err := coordinator.manager.Unblock(registered.ContainerID); err != nil {
		return err
	}
	return coordinator.removeCandidate(rollbackCtx, candidateID)
}

func (coordinator *UpgradeCoordinator) removeCandidate(ctx context.Context, candidateID string) error {
	if strings.TrimSpace(candidateID) == "" {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), coordinator.config.RollbackTimeout)
	defer cancel()
	return coordinator.docker.RemoveContainer(cleanupCtx, candidateID)
}

func newCandidateContainerName(activeName string) (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate candidate container name: %w", err)
	}
	base := strings.Trim(strings.TrimSpace(activeName), "-_.")
	if base == "" {
		base = "model"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	return fmt.Sprintf("%s-candidate-%s", base, hex.EncodeToString(random)), nil
}

func upgradeFailure(stage UpgradeStage, err, rollbackErr error) error {
	if err == nil {
		err = errors.New("unknown model upgrade failure")
	}
	return &UpgradeError{Stage: stage, Err: err, RollbackErr: rollbackErr}
}

func upgradeRollbackFailure(stage UpgradeStage, err, rollbackErr error) error {
	upgradeErr := upgradeFailure(stage, err, rollbackErr).(*UpgradeError)
	upgradeErr.RollbackAttempted = true
	return upgradeErr
}

var _ UpgradeManager = (*modelmanager.Manager)(nil)
