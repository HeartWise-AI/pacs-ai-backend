package modelupgrade

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

type UpgradeStage string

const (
	UpgradeStageResolveActive UpgradeStage = "resolve_active"
	UpgradeStagePull          UpgradeStage = "pull"
	UpgradeStageCreate        UpgradeStage = "create_candidate"
	UpgradeStageValidate      UpgradeStage = "validate_candidate"
	UpgradeStageDrain         UpgradeStage = "drain_active"
	UpgradeStagePrepare       UpgradeStage = "prepare_candidate"
	UpgradeStageActivate      UpgradeStage = "activate_candidate"
)

// UpgradeError identifies the failed stage and reports rollback separately.
type UpgradeError struct {
	Stage       UpgradeStage
	Err         error
	RollbackErr error
}

func (err *UpgradeError) Error() string {
	if err.RollbackErr != nil {
		return fmt.Sprintf("model upgrade failed at %s: %v; rollback: %v", err.Stage, err.Err, err.RollbackErr)
	}
	return fmt.Sprintf("model upgrade failed at %s: %v", err.Stage, err.Err)
}

func (err *UpgradeError) Unwrap() error { return err.Err }

type UpgradeRequest struct {
	TenantID       string
	ModelID        string
	ImageReference string
	ExpectedDigest string
}

type UpgradeResult struct {
	Previous      entity.InferenceModelDeployment
	Active        entity.InferenceModelDeployment
	ContainerName string
}

type CoordinatorConfig struct {
	DrainTimeout    time.Duration
	RollbackTimeout time.Duration
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
	registrations UpgradeRegistrationRepository,
	config CoordinatorConfig,
) (*UpgradeCoordinator, error) {
	if dockerClient == nil || apiClient == nil || validator == nil || manager == nil || models == nil || registrations == nil {
		return nil, errors.New("model upgrade coordinator dependencies are required")
	}
	if config.DrainTimeout <= 0 {
		config.DrainTimeout = 5 * time.Minute
	}
	if config.RollbackTimeout <= 0 {
		config.RollbackTimeout = 5 * time.Minute
	}
	return &UpgradeCoordinator{
		docker: dockerClient, api: apiClient, validator: validator, manager: manager,
		models: models, registrations: registrations, config: config, now: time.Now,
		inFlight: make(map[string]struct{}),
	}, nil
}

// Upgrade stages and validates a candidate before closing active admission. It
// does not remove the old container after success; that rollback baseline is
// intentionally retained for a later cleanup step.
func (coordinator *UpgradeCoordinator) Upgrade(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ModelID = strings.TrimSpace(request.ModelID)
	request.ImageReference = strings.TrimSpace(request.ImageReference)
	request.ExpectedDigest = strings.TrimSpace(request.ExpectedDigest)
	if request.TenantID == "" || request.ModelID == "" || request.ImageReference == "" {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New(apiError.InvalidPayload), nil)
	}
	upgradeKey := request.TenantID + "\x00" + request.ModelID
	if !coordinator.beginUpgrade(upgradeKey) {
		return UpgradeResult{}, upgradeFailure(
			UpgradeStageResolveActive, errors.New(apiError.InferenceUpgradeConflict), nil,
		)
	}
	defer coordinator.endUpgrade(upgradeKey)

	registered, err := coordinator.models.SelectInferenceModelByID(ctx, request.ModelID)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, err, nil)
	}
	if registered.TenantID != request.TenantID {
		return UpgradeResult{}, upgradeFailure(UpgradeStageResolveActive, errors.New(apiError.MissingRecord), nil)
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

	normalizedImage, err := coordinator.validator.ValidateImageReference(request.ImageReference, request.ExpectedDigest)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStagePull, err, nil)
	}
	if err = coordinator.docker.PullImage(ctx, normalizedImage); err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStagePull, err, nil)
	}
	resolvedImage, err := coordinator.validator.ResolveImage(ctx, normalizedImage, request.ExpectedDigest)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStagePull, err, nil)
	}
	candidateName, err := newCandidateContainerName(activeName)
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageCreate, err, nil)
	}
	candidateID, err := coordinator.docker.CreateCandidateContainer(ctx, docker.CreateCandidateContainer{
		Name: candidateName, Image: resolvedImage.LocalImageID,
		Envs: append([]string(nil), registered.Envs...), ActiveContainerID: registered.ContainerID,
	})
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

	validated, err := coordinator.validator.ValidateContainer(ctx, ValidateCandidateContainer{
		ContainerID: candidateID, Image: resolvedImage, ActiveModelInfo: activeModelInfo.Data,
		RegisteredOutputMode: registered.OutputMode,
	})
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, err, coordinator.removeCandidate(ctx, candidateID))
	}
	deployment, err := DeploymentFromCandidate(validated, coordinator.now())
	if err != nil {
		return UpgradeResult{}, upgradeFailure(UpgradeStageValidate, err, coordinator.removeCandidate(ctx, candidateID))
	}

	drainCtx, cancelDrain := context.WithTimeout(ctx, coordinator.config.DrainTimeout)
	err = coordinator.manager.BlockAndDrain(drainCtx, registered.ContainerID)
	cancelDrain()
	if err != nil {
		rollbackErr := errors.Join(coordinator.manager.Unblock(registered.ContainerID), coordinator.removeCandidate(ctx, candidateID))
		return UpgradeResult{}, upgradeFailure(UpgradeStageDrain, err, rollbackErr)
	}
	if err = coordinator.manager.ReplaceDrained(registered.ContainerID, candidateID); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, false)
		return UpgradeResult{}, upgradeFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.manager.PrepareBlocked(ctx, candidateID); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.verifyPreparedCandidate(ctx, validated); err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeFailure(
			UpgradeStagePrepare, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}

	previous, err := coordinator.registrations.ReplaceInferenceModelRegistration(ctx, repositoryTypes.ReplaceInferenceModelRegistration{
		ID: request.ModelID, TenantID: request.TenantID,
		ExpectedContainerID: registered.ContainerID, ExpectedImageReference: registered.DockerImage,
		Deployment: deployment,
	})
	if err != nil {
		rollbackErr := coordinator.rollbackBeforeActivation(ctx, registered.ContainerID, candidateID, true)
		return UpgradeResult{}, upgradeFailure(
			UpgradeStageActivate, err, coordinator.cleanupAfterRollback(ctx, candidateID, rollbackErr),
		)
	}
	if err = coordinator.manager.Unblock(candidateID); err != nil {
		rollbackErr := coordinator.rollbackActivated(ctx, request, registered, deployment, previous, candidateID)
		return UpgradeResult{}, upgradeFailure(UpgradeStageActivate, err, rollbackErr)
	}

	return UpgradeResult{Previous: previous, Active: deployment, ContainerName: validated.ContainerName}, nil
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

var _ UpgradeManager = (*modelmanager.Manager)(nil)
