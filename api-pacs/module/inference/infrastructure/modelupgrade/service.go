package modelupgrade

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/ksuid"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/containerready"
)

type LifecycleAPI interface {
	containerready.Metadata
	GetModelRuntime(context.Context, string) (api.ModelRuntime, error)
	LoadModel(context.Context, string) (api.ModelRuntime, error)
	UnloadModel(context.Context, string) (api.ModelRuntime, error)
}

type UpgradeCoordinator interface {
	BlockAndDrain(context.Context, string) error
	ReplaceDrained(string, string) error
	PrepareBlocked(context.Context, string) error
	Unblock(string) error
}

type Service struct {
	Repository repository.InferenceModelUpgradeRepositoryInterface
	Docker     docker.ModelUpgradeDockerSDKInterface
	API        LifecycleAPI
	Manager    UpgradeCoordinator
	Config     Config
	Metrics    Metrics

	Now      func() time.Time
	NewID    func() string
	Launch   func(func())
	Schedule func(time.Duration, func())

	initOnce          sync.Once
	initErr           error
	mu                sync.Mutex
	running           map[string]*runningUpgrade
	cleanupScheduled  map[string]bool
	persistScheduled  map[string]bool
	recoveryScheduled map[string]bool
}

type runningUpgrade struct {
	cancel     context.CancelFunc
	committing bool
}

type upgradeOperation struct {
	attempt        entity.InferenceModelUpgrade
	model          entity.InferenceModel
	image          validatedImageReference
	candidate      *entity.InferenceModelDeployment
	oldBlocked     bool
	managerSwapped bool
	activated      bool
	startedAt      time.Time
	previousInfo   *api.ModelInfo
}

func (service *Service) defaults() error {
	service.initOnce.Do(func() {
		if service.Repository == nil || service.Docker == nil || service.API == nil || service.Manager == nil {
			service.initErr = errors.New("model upgrade dependencies are incomplete")
			return
		}
		if err := service.Config.Validate(); err != nil {
			service.initErr = err
			return
		}
		if service.Now == nil {
			service.Now = time.Now
		}
		if service.NewID == nil {
			service.NewID = func() string { return ksuid.New().String() }
		}
		if service.Launch == nil {
			service.Launch = func(run func()) { go run() }
		}
		if service.Schedule == nil {
			service.Schedule = func(delay time.Duration, run func()) { time.AfterFunc(delay, run) }
		}
		if service.Metrics == nil {
			service.Metrics = defaultMetrics
		}
		service.running = make(map[string]*runningUpgrade)
		service.cleanupScheduled = make(map[string]bool)
		service.persistScheduled = make(map[string]bool)
		service.recoveryScheduled = make(map[string]bool)
	})
	return service.initErr
}

func (service *Service) StartInferenceModelUpgrade(
	ctx context.Context,
	request application.StartInferenceModelUpgrade,
) (entity.InferenceModelUpgrade, error) {
	if err := service.defaults(); err != nil {
		return entity.InferenceModelUpgrade{}, err
	}
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ModelID = strings.TrimSpace(request.ModelID)
	request.ActorUserID = strings.TrimSpace(request.ActorUserID)
	if request.TenantID == "" || request.ModelID == "" || request.ActorUserID == "" {
		return entity.InferenceModelUpgrade{}, invalid("Tenant, model, and actor identifiers are required.")
	}
	image, err := validateImageReference(request.ImageReference, request.ExpectedDigest, service.Config.AllowedNamespace)
	if err != nil {
		return entity.InferenceModelUpgrade{}, err
	}
	drainTimeout := time.Duration(request.DrainTimeoutSeconds) * time.Second
	if request.DrainTimeoutSeconds == 0 {
		drainTimeout = 5 * time.Minute
		if service.Config.MaxDrainTimeout < drainTimeout {
			drainTimeout = service.Config.MaxDrainTimeout
		}
	}
	if drainTimeout <= 0 || drainTimeout > service.Config.MaxDrainTimeout {
		return entity.InferenceModelUpgrade{}, invalid("drainTimeoutSeconds is outside the permitted range.")
	}

	startedAt := service.Now().UTC()
	now := startedAt.Unix()
	attempt := entity.InferenceModelUpgrade{
		ID:                      service.NewID(),
		TenantID:                request.TenantID,
		ModelID:                 request.ModelID,
		ActorUserID:             request.ActorUserID,
		State:                   entity.InferenceModelUpgradeQueued,
		RequestedImageReference: image.Normalized,
		ExpectedDigest:          image.Digest,
		DrainTimeoutSeconds:     int(drainTimeout / time.Second),
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	requestedAttempt := attempt
	attempt, model, err := service.Repository.BeginInferenceModelUpgrade(ctx, attempt)
	if err != nil {
		reconcileCtx, cancelReconcile := context.WithTimeout(context.Background(), service.Config.ReadinessTimeout)
		persisted, registered, reconciled := service.reconcileBegunUpgrade(reconcileCtx, requestedAttempt)
		cancelReconcile()
		if reconciled {
			attempt = persisted
			model = registered
		} else {
			if err.Error() == apiError.DuplicateRecord {
				return entity.InferenceModelUpgrade{}, conflict()
			}
			if err.Error() == apiError.InferenceUpgradeConflict {
				return entity.InferenceModelUpgrade{}, sharedContainerConflict()
			}
			if err.Error() == apiError.MissingRecord {
				return entity.InferenceModelUpgrade{}, notFound()
			}
			return entity.InferenceModelUpgrade{}, err
		}
	}
	if attempt.Previous.ContainerID == "" {
		attempt.State = entity.InferenceModelUpgradeFailed
		attempt.ErrorCode = apiError.InferenceUpgradeInvalid
		attempt.ErrorMessage = "The registered model has no active container identity."
		attempt.CompletedAt = service.Now().UTC().Unix()
		attempt.UpdatedAt = attempt.CompletedAt
		operation := &upgradeOperation{attempt: attempt, model: model, startedAt: startedAt}
		finishCtx, cancelFinish := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
		finishErr := service.finishFailureWithRetry(finishCtx, operation, nil)
		cancelFinish()
		if finishErr != nil {
			return entity.InferenceModelUpgrade{}, finishErr
		}
		return entity.InferenceModelUpgrade{}, invalid("The registered model has no active container identity.")
	}
	service.Metrics.ObserveTransition(entity.InferenceModelUpgradeQueued)

	operationContext, cancel := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
	service.mu.Lock()
	service.running[attempt.ID] = &runningUpgrade{cancel: cancel}
	service.mu.Unlock()
	service.Launch(func() {
		defer cancel()
		defer service.removeRunning(attempt.ID)
		service.execute(operationContext, &upgradeOperation{attempt: attempt, model: model, image: image, startedAt: startedAt})
	})
	return attempt, nil
}

func (service *Service) reconcileBegunUpgrade(
	ctx context.Context,
	requested entity.InferenceModelUpgrade,
) (entity.InferenceModelUpgrade, entity.InferenceModel, bool) {
	for {
		persisted, attemptErr := service.Repository.GetInferenceModelUpgrade(
			ctx, requested.TenantID, requested.ModelID, requested.ID,
		)
		if attemptErr != nil && attemptErr.Error() == apiError.MissingRecord {
			return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, false
		}
		if attemptErr == nil {
			registered, modelErr := service.Repository.GetInferenceModelForUpgrade(
				ctx, requested.TenantID, requested.ModelID,
			)
			if modelErr != nil && modelErr.Error() == apiError.MissingRecord {
				return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, false
			}
			if modelErr == nil {
				return persisted, registered, registered.ActiveUpgradeID == requested.ID
			}
		}
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return entity.InferenceModelUpgrade{}, entity.InferenceModel{}, false
		case <-timer.C:
		}
	}
}

func (service *Service) GetInferenceModelUpgrade(
	ctx context.Context,
	tenantID, modelID, upgradeID string,
) (entity.InferenceModelUpgrade, error) {
	if err := service.defaults(); err != nil {
		return entity.InferenceModelUpgrade{}, err
	}
	upgrade, err := service.Repository.GetInferenceModelUpgrade(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(modelID), strings.TrimSpace(upgradeID))
	if err != nil && err.Error() == apiError.MissingRecord {
		return entity.InferenceModelUpgrade{}, notFound()
	}
	return upgrade, err
}

func (service *Service) CancelInferenceModelUpgrade(
	ctx context.Context,
	tenantID, modelID, upgradeID string,
) (entity.InferenceModelUpgrade, error) {
	upgrade, err := service.GetInferenceModelUpgrade(ctx, tenantID, modelID, upgradeID)
	if err != nil {
		return upgrade, err
	}
	if upgrade.State.Terminal() {
		return entity.InferenceModelUpgrade{}, cancellationConflict()
	}
	service.mu.Lock()
	running := service.running[upgrade.ID]
	if running == nil || running.committing {
		service.mu.Unlock()
		return entity.InferenceModelUpgrade{}, cancellationConflict()
	}
	running.cancel()
	service.mu.Unlock()
	return upgrade, nil
}

func (service *Service) RecoverInferenceModelUpgrades(ctx context.Context) error {
	if err := service.defaults(); err != nil {
		return err
	}
	upgrades, err := service.Repository.ListRecoverableInferenceModelUpgrades(ctx)
	if err != nil {
		return err
	}
	for index := range upgrades {
		attempt := upgrades[index]
		service.mu.Lock()
		_, alreadyRunning := service.running[attempt.ID]
		service.mu.Unlock()
		if alreadyRunning {
			continue
		}
		var model entity.InferenceModel
		if !attempt.State.Terminal() || attempt.State == entity.InferenceModelUpgradeDegraded {
			var modelErr error
			model, modelErr = service.Repository.GetInferenceModelForUpgrade(ctx, attempt.TenantID, attempt.ModelID)
			if modelErr != nil {
				if attempt.State == entity.InferenceModelUpgradeDegraded && modelErr.Error() == apiError.MissingRecord {
					service.recoverCleanup(ctx, attempt)
					continue
				}
				return modelErr
			}
		}
		if attempt.State == entity.InferenceModelUpgradeDegraded {
			if model.ActiveUpgradeID == attempt.ID {
				recoveryContext, cancel := context.WithTimeout(ctx, service.Config.OperationTimeout)
				err = service.blockAndDrainExclusive(recoveryContext, attempt.ModelID, model.ContainerID)
				cancel()
				if err != nil {
					return fmt.Errorf("degraded model admission could not be blocked: %w", err)
				}
				continue
			}
			service.recoverCleanup(ctx, attempt)
			continue
		}
		recoveryContext, cancel := context.WithTimeout(ctx, service.Config.OperationTimeout)
		service.mu.Lock()
		service.running[attempt.ID] = &runningUpgrade{cancel: cancel, committing: true}
		service.mu.Unlock()
		// Startup recovery is an admission-safety barrier. Returning before the
		// registered deployment is reconciled could expose an unverified candidate.
		func() {
			defer cancel()
			defer service.removeRunning(attempt.ID)
			service.recover(recoveryContext, attempt, model)
		}()
	}
	return nil
}

func (service *Service) execute(ctx context.Context, operation *upgradeOperation) {
	if err := service.transition(ctx, operation, entity.InferenceModelUpgradePulling); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	if err := service.Docker.PullImage(ctx, operation.image.Normalized); err != nil {
		service.fail(ctx, operation, fmt.Errorf("candidate image pull failed: %w", err))
		return
	}
	inspection, err := service.Docker.InspectImage(ctx, operation.image.Normalized)
	if err != nil {
		service.fail(ctx, operation, fmt.Errorf("candidate image inspection failed: %w", err))
		return
	}
	resolvedDigest, err := resolveImageDigest(operation.image, inspection)
	if err != nil {
		service.fail(ctx, operation, err)
		return
	}
	if err = service.transition(ctx, operation, entity.InferenceModelUpgradeValidating); err != nil {
		service.fail(ctx, operation, err)
		return
	}

	activeName, activeInfo, err := containerready.Ensure(
		ctx, service.Docker, service.API, operation.attempt.Previous.ContainerID,
		service.Config.ReadinessTimeout, service.Config.PollInterval,
	)
	if err != nil {
		service.fail(ctx, operation, fmt.Errorf("active model identity could not be verified: %w", err))
		return
	}
	operation.previousInfo = &activeInfo
	operation.attempt.Previous.ModelVersion = activeInfo.Version
	if activeName != "" && activeName != operation.attempt.Previous.ContainerID {
		operation.attempt.PreviousAliases = []string{activeName}
	}
	operation.attempt.PreviousSupportedOutputModes = append(
		[]string(nil), activeInfo.SupportedOutputModes...,
	)

	candidateNameHint := candidateContainerName(operation.attempt.ID)
	candidateID, err := service.Docker.CreateContainer(ctx, docker.CreateContainer{
		Name:                candidateNameHint,
		Image:               inspection.ID,
		Envs:                append([]string(nil), operation.model.Envs...),
		TemplateContainerID: operation.attempt.Previous.ContainerID,
	})
	if err != nil {
		// Docker may create the container and lose the response. Resolve the
		// deterministic name so failure finalization can remove it or persist a
		// pending cleanup identity instead of orphaning the candidate.
		lookupTimeout := service.Config.ReadinessTimeout
		if lookupTimeout > 10*time.Second {
			lookupTimeout = 10 * time.Second
		}
		lookupCtx, cancelLookup := context.WithTimeout(context.Background(), lookupTimeout)
		createdID := candidateNameHint
		if created, lookupErr := service.discoverCandidateContainer(lookupCtx, candidateNameHint); lookupErr == nil &&
			created.ID != "" && created.ID != operation.attempt.Previous.ContainerID {
			createdID = created.ID
		}
		operation.candidate = &entity.InferenceModelDeployment{
			ContainerID: createdID, ImageReference: operation.image.Normalized,
			ImageDigest: resolvedDigest, LocalImageID: inspection.ID,
		}
		operation.attempt.Candidate = operation.candidate
		_ = service.Repository.SaveInferenceModelUpgrade(lookupCtx, operation.attempt)
		cancelLookup()
		service.fail(ctx, operation, fmt.Errorf("candidate container creation failed: %w", err))
		return
	}
	operation.candidate = &entity.InferenceModelDeployment{
		ContainerID:    candidateID,
		ImageReference: operation.image.Normalized,
		ImageDigest:    resolvedDigest,
		LocalImageID:   inspection.ID,
	}
	operation.attempt.Candidate = operation.candidate
	if err = service.Repository.SaveInferenceModelUpgrade(ctx, operation.attempt); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	candidateName, candidateInfo, err := containerready.Ensure(
		ctx, service.Docker, service.API, candidateID, service.Config.ReadinessTimeout, service.Config.PollInterval,
	)
	if err != nil {
		service.fail(ctx, operation, err)
		return
	}
	provenance, err := validateCandidateIdentity(
		operation.image, resolvedDigest, inspection, activeInfo, candidateInfo, operation.model.OutputMode,
	)
	if err != nil {
		service.fail(ctx, operation, err)
		return
	}
	operation.candidate.ModelVersion = candidateInfo.Version
	operation.candidate.Provenance = provenance
	operation.attempt.Candidate = operation.candidate
	operation.attempt.CandidateSupportedOutputModes = append(
		[]string(nil), candidateInfo.SupportedOutputModes...,
	)
	if err = service.Repository.SaveInferenceModelUpgrade(ctx, operation.attempt); err != nil {
		service.fail(ctx, operation, err)
		return
	}

	if err = service.transition(ctx, operation, entity.InferenceModelUpgradeDraining); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	drainContext, cancelDrain := context.WithTimeout(ctx, time.Duration(operation.attempt.DrainTimeoutSeconds)*time.Second)
	err = service.ensureExclusiveContainer(drainContext, operation.attempt.ModelID, operation.attempt.Previous.ContainerID)
	if err != nil {
		cancelDrain()
		service.fail(ctx, operation, fmt.Errorf("active container cannot be drained safely: %w", err))
		return
	}
	operation.oldBlocked = true
	drainStarted := time.Now()
	err = service.Manager.BlockAndDrain(drainContext, operation.attempt.Previous.ContainerID)
	cancelDrain()
	if err != nil {
		service.Metrics.ObserveDrain("failed", time.Since(drainStarted))
		service.fail(ctx, operation, fmt.Errorf("active inference did not drain safely: %w", err))
		return
	}
	service.Metrics.ObserveDrain("success", time.Since(drainStarted))
	if err = service.transition(ctx, operation, entity.InferenceModelUpgradeActivating); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	if err = service.Manager.ReplaceDrained(operation.attempt.Previous.ContainerID, candidateID); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	operation.managerSwapped = true
	if err = service.Manager.PrepareBlocked(ctx, candidateID); err != nil {
		service.fail(ctx, operation, fmt.Errorf("candidate model load failed: %w", err))
		return
	}
	operation.candidate.DeployedAt = service.Now().UTC().Unix()
	if !validTransition(operation.attempt.State, entity.InferenceModelUpgradeVerifying) {
		service.fail(ctx, operation, errors.New("invalid activation state transition"))
		return
	}
	operation.attempt.State = entity.InferenceModelUpgradeVerifying
	operation.attempt.UpdatedAt = operation.candidate.DeployedAt
	service.Metrics.ObserveTransition(entity.InferenceModelUpgradeVerifying)
	if err = service.Repository.ActivateAndRetargetInferenceModelUpgrade(
		ctx,
		operation.attempt,
		*operation.candidate,
		candidateInfo.SupportedOutputModes,
		operation.attempt.Previous.ContainerID,
		operation.attempt.PreviousAliases,
	); err != nil {
		// Firestore transactions are atomic, but a transport failure can make the
		// client uncertain about the observed result. Re-read before rollback so
		// registration restoration is never skipped after a committed switch.
		readCtx, cancelRead := context.WithTimeout(context.Background(), service.Config.ReadinessTimeout)
		registered, readErr := service.Repository.GetInferenceModelForUpgrade(readCtx, operation.attempt.TenantID, operation.attempt.ModelID)
		cancelRead()
		if readErr != nil {
			operation.attempt.FailureStage = operation.attempt.State
			service.Metrics.ObserveFailure(operation.attempt.State)
			service.degrade(operation, errors.New("activation outcome could not be determined"))
			return
		}
		if registered.ContainerID == candidateID {
			operation.activated = true
		} else if registered.ContainerID != operation.attempt.Previous.ContainerID {
			operation.attempt.FailureStage = operation.attempt.State
			service.Metrics.ObserveFailure(operation.attempt.State)
			service.degrade(operation, errors.New("activation outcome does not match either deployment"))
			return
		}
		service.fail(ctx, operation, err)
		return
	}
	operation.activated = true

	if err = service.verifyActivated(ctx, candidateID, candidateName, candidateInfo); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	if err = service.beginCommit(ctx, operation.attempt.ID); err != nil {
		service.fail(ctx, operation, err)
		return
	}
	if err = service.Manager.Unblock(candidateID); err != nil {
		service.fail(ctx, operation, errors.New("candidate admission could not be reopened"))
		return
	}
	operation.attempt.State = entity.InferenceModelUpgradeSucceeded
	operation.attempt.UpdatedAt = service.Now().UTC().Unix()
	operation.attempt.CompletedAt = operation.attempt.UpdatedAt
	operation.attempt.PendingCleanupContainerID = operation.attempt.Previous.ContainerID
	commitCtx, cancelCommit := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
	err = service.finishSuccessWithRetry(commitCtx, operation)
	cancelCommit()
	if err != nil {
		if err.Error() == apiError.InferenceUpgradeInvalid {
			service.fail(ctx, operation, err)
			return
		}
		// Do not roll back or remove either container while the durable commit
		// outcome is unknown. A committed attempt remains eligible for cleanup;
		// an uncommitted attempt remains locked and recoverable on restart.
		log.Printf("[model-upgrade] upgrade_id=%s model_id=%s success_finalization_uncertain=%v", operation.attempt.ID, operation.attempt.ModelID, err)
		service.scheduleRecovery(operation.attempt)
		return
	}
	service.Metrics.ObserveCompletion(operation.attempt.State, service.elapsed(operation))
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
	err = service.waitForProcessingJobs(
		cleanupCtx, operation.attempt, operation.attempt.Previous.ModelVersion,
	)
	if err == nil {
		err = service.removeUnregisteredContainer(cleanupCtx, operation.attempt.Previous.ContainerID)
	}
	cancelCleanup()
	if err != nil {
		log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=succeeded previous_cleanup=pending", operation.attempt.ID, operation.attempt.ModelID)
		service.scheduleCleanup(operation.attempt)
	} else {
		operation.attempt.PendingCleanupContainerID = ""
		saveCtx, cancelSave := context.WithTimeout(context.Background(), service.Config.ReadinessTimeout)
		saveErr := service.Repository.SaveInferenceModelUpgrade(saveCtx, operation.attempt)
		cancelSave()
		if saveErr != nil {
			log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=succeeded cleanup_commit=pending", operation.attempt.ID, operation.attempt.ModelID)
			service.scheduleCleanup(operation.attempt)
		}
	}
	log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=succeeded digest=%s", operation.attempt.ID, operation.attempt.ModelID, operation.candidate.ImageDigest)
}

func (service *Service) verifyActivated(
	ctx context.Context,
	candidateID, candidateName string,
	expected api.ModelInfo,
) error {
	container, err := service.Docker.GetContainerInfo(ctx, candidateID)
	if err != nil || !container.Running {
		return errors.New("activated candidate container is not running")
	}
	metadata, err := service.API.GetModelInfo(ctx, candidateName)
	if err != nil || !metadata.Success || metadata.Data.ModelID != expected.ModelID || metadata.Data.Version != expected.Version {
		return errors.New("activated candidate metadata smoke test failed")
	}
	runtime, err := service.API.GetModelRuntime(ctx, candidateName)
	if err != nil || runtime.State != api.RuntimeReady || !runtime.Loaded || runtime.ActiveRequests != 0 {
		return errors.New("activated candidate lifecycle smoke test failed")
	}
	return nil
}

func (service *Service) discoverCandidateContainer(ctx context.Context, name string) (docker.GetContainerInfoResult, error) {
	var lastErr error
	for {
		container, err := service.Docker.GetContainerInfo(ctx, name)
		if err == nil {
			return container, nil
		}
		lastErr = err
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return docker.GetContainerInfoResult{}, errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func (service *Service) transition(ctx context.Context, operation *upgradeOperation, state entity.InferenceModelUpgradeState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validTransition(operation.attempt.State, state) {
		return fmt.Errorf("invalid model upgrade transition from %s to %s", operation.attempt.State, state)
	}
	operation.attempt.State = state
	operation.attempt.UpdatedAt = service.Now().UTC().Unix()
	service.Metrics.ObserveTransition(state)
	log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=%s elapsed_ms=%d", operation.attempt.ID, operation.attempt.ModelID, state, service.elapsed(operation).Milliseconds())
	return service.Repository.SaveInferenceModelUpgrade(ctx, operation.attempt)
}

func (service *Service) fail(ctx context.Context, operation *upgradeOperation, cause error) {
	// Cancellation and failure finalization share the same mutex. If cancellation
	// wins, preserve it in the terminal outcome; otherwise cancellation is rejected
	// once rollback has become externally observable.
	service.mu.Lock()
	if running := service.running[operation.attempt.ID]; running != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			cause = errors.Join(cause, ctxErr)
		}
		running.committing = true
	}
	service.mu.Unlock()
	log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=%s failed=%v", operation.attempt.ID, operation.attempt.ModelID, operation.attempt.State, cause)
	failedState := operation.attempt.State
	operation.attempt.FailureStage = failedState
	service.Metrics.ObserveFailure(failedState)
	rollbackContext, cancel := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
	defer cancel()
	if operation.managerSwapped {
		operation.attempt.State = entity.InferenceModelUpgradeRollingBack
		operation.attempt.UpdatedAt = service.Now().UTC().Unix()
		_ = service.Repository.SaveInferenceModelUpgrade(rollbackContext, operation.attempt)
		if err := service.rollbackManager(rollbackContext, operation); err != nil {
			service.degrade(operation, err)
			return
		}
		operation.attempt.RollbackOutcome = "succeeded"
	} else if operation.oldBlocked {
		if err := service.Manager.Unblock(operation.attempt.Previous.ContainerID); err != nil {
			service.degrade(operation, err)
			return
		}
		operation.attempt.RollbackOutcome = "not_required"
	} else {
		operation.attempt.RollbackOutcome = "not_required"
	}
	if operation.candidate != nil {
		operation.attempt.PendingCleanupContainerID = operation.candidate.ContainerID
	}
	operation.attempt.ErrorCode = apiError.InferenceUpgradeFailed
	operation.attempt.ErrorMessage = failureMessage(failedState)
	if errors.Is(cause, context.Canceled) {
		operation.attempt.State = entity.InferenceModelUpgradeCancelled
	} else if operation.activated {
		operation.attempt.State = entity.InferenceModelUpgradeRolledBack
	} else {
		operation.attempt.State = entity.InferenceModelUpgradeFailed
	}
	operation.attempt.UpdatedAt = service.Now().UTC().Unix()
	operation.attempt.CompletedAt = operation.attempt.UpdatedAt
	var restored *entity.InferenceModelDeployment
	if operation.activated {
		restored = &operation.attempt.Previous
	}
	if err := service.finishFailureWithRetry(rollbackContext, operation, restored); err != nil {
		if err.Error() == apiError.InferenceUpgradeInvalid && restored != nil {
			blockErr := service.blockAndDrainExclusive(rollbackContext, operation.attempt.ModelID, restored.ContainerID)
			if blockErr != nil {
				err = errors.Join(err, fmt.Errorf("incompatible restored deployment could not be blocked: %w", blockErr))
			}
			service.degrade(operation, err)
			return
		}
		log.Printf("[model-upgrade] upgrade_id=%s model_id=%s finalization_failed=%v", operation.attempt.ID, operation.attempt.ModelID, err)
		service.scheduleRecovery(operation.attempt)
		return
	}
	service.Metrics.ObserveCompletion(operation.attempt.State, service.elapsed(operation))
	if operation.candidate != nil {
		service.cleanupFailedCandidate(operation)
	}
}

func (service *Service) cleanupFailedCandidate(operation *upgradeOperation) {
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
	defer cancelCleanup()
	var cleanupErr error
	if operation.activated {
		cleanupErr = service.waitForProcessingJobs(
			cleanupCtx, operation.attempt, operation.candidate.ModelVersion,
		)
	}
	if cleanupErr == nil {
		cleanupErr = service.removeUnregisteredContainer(cleanupCtx, operation.candidate.ContainerID)
	}
	if cleanupErr != nil {
		log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=%s candidate_cleanup=pending", operation.attempt.ID, operation.attempt.ModelID, operation.attempt.State)
		service.scheduleCleanup(operation.attempt)
		return
	}
	operation.attempt.PendingCleanupContainerID = ""
	operation.attempt.UpdatedAt = service.Now().UTC().Unix()
	saveCtx, cancelSave := context.WithTimeout(context.Background(), service.Config.ReadinessTimeout)
	defer cancelSave()
	if err := service.Repository.SaveInferenceModelUpgrade(saveCtx, operation.attempt); err != nil {
		log.Printf("[model-upgrade] upgrade_id=%s model_id=%s state=%s candidate_cleanup_commit=pending", operation.attempt.ID, operation.attempt.ModelID, operation.attempt.State)
		service.scheduleCleanup(operation.attempt)
	}
}

func (service *Service) finishFailureWithRetry(
	ctx context.Context,
	operation *upgradeOperation,
	restored *entity.InferenceModelDeployment,
) error {
	var lastErr error
	var supportedOutputModes []string
	if restored != nil {
		supportedOutputModes = operation.attempt.PreviousSupportedOutputModes
	}
	for {
		lastErr = service.Repository.FinishInferenceModelUpgrade(
			ctx, operation.attempt, restored, supportedOutputModes,
		)
		if lastErr == nil || service.failureCommitConfirmed(ctx, operation, restored) {
			return nil
		}
		if lastErr.Error() == apiError.InferenceUpgradeInvalid {
			return lastErr
		}
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("failure finalization retry exhausted: %w", lastErr)
		case <-timer.C:
		}
	}
}

func (service *Service) beginCommit(ctx context.Context, upgradeID string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	running := service.running[upgradeID]
	if running == nil {
		return errors.New("upgrade operation is no longer running")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	running.committing = true
	return nil
}

func (service *Service) finishSuccessWithRetry(ctx context.Context, operation *upgradeOperation) error {
	var lastErr error
	for {
		lastErr = service.Repository.FinishInferenceModelUpgrade(
			ctx, operation.attempt, nil, operation.attempt.CandidateSupportedOutputModes,
		)
		if lastErr == nil || service.successCommitConfirmed(ctx, operation) {
			return nil
		}
		if lastErr.Error() == apiError.InferenceUpgradeInvalid {
			return lastErr
		}
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("success finalization outcome remains uncertain: %w", lastErr)
		case <-timer.C:
		}
	}
}

func (service *Service) failureCommitConfirmed(
	ctx context.Context,
	operation *upgradeOperation,
	restored *entity.InferenceModelDeployment,
) bool {
	registered, modelErr := service.Repository.GetInferenceModelForUpgrade(ctx, operation.attempt.TenantID, operation.attempt.ModelID)
	persisted, attemptErr := service.Repository.GetInferenceModelUpgrade(
		ctx, operation.attempt.TenantID, operation.attempt.ModelID, operation.attempt.ID,
	)
	if modelErr != nil || attemptErr != nil || registered.ActiveUpgradeID != "" || persisted.State != operation.attempt.State {
		return false
	}
	expectedContainerID := operation.attempt.Previous.ContainerID
	if restored != nil {
		expectedContainerID = restored.ContainerID
	}
	return registered.ContainerID == expectedContainerID
}

func (service *Service) rollbackManager(ctx context.Context, operation *upgradeOperation) error {
	if operation.candidate == nil {
		return errors.New("candidate identity is unavailable for rollback")
	}
	if err := service.blockAndDrainExclusive(ctx, operation.attempt.ModelID, operation.candidate.ContainerID); err != nil {
		return fmt.Errorf("candidate drain during rollback failed: %w", err)
	}
	if err := service.Manager.ReplaceDrained(operation.candidate.ContainerID, operation.attempt.Previous.ContainerID); err != nil {
		return fmt.Errorf("manager rollback replacement failed: %w", err)
	}
	if err := service.Manager.PrepareBlocked(ctx, operation.attempt.Previous.ContainerID); err != nil {
		return fmt.Errorf("previous model reload failed: %w", err)
	}
	if operation.activated {
		operation.attempt.State = entity.InferenceModelUpgradeRollingBack
		operation.attempt.UpdatedAt = service.Now().UTC().Unix()
		if err := service.activateRollbackWithRetry(ctx, operation); err != nil {
			return fmt.Errorf("registration rollback failed: %w", err)
		}
	}
	if err := service.verifyRestored(ctx, operation); err != nil {
		return err
	}
	if err := service.Manager.Unblock(operation.attempt.Previous.ContainerID); err != nil {
		return fmt.Errorf("previous model admission could not be restored: %w", err)
	}
	return nil
}

func (service *Service) activateRollbackWithRetry(ctx context.Context, operation *upgradeOperation) error {
	var lastErr error
	for {
		lastErr = service.Repository.ActivateAndRetargetInferenceModelUpgrade(
			ctx,
			operation.attempt,
			operation.attempt.Previous,
			operation.attempt.PreviousSupportedOutputModes,
			operation.candidate.ContainerID,
			operation.attempt.PreviousAliases,
		)
		if lastErr == nil {
			return nil
		}
		if lastErr.Error() == apiError.InferenceUpgradeInvalid {
			return lastErr
		}
		registered, readErr := service.Repository.GetInferenceModelForUpgrade(
			ctx, operation.attempt.TenantID, operation.attempt.ModelID,
		)
		if readErr == nil &&
			registered.ContainerID == operation.attempt.Previous.ContainerID &&
			registered.ActiveUpgradeID == operation.attempt.ID {
			if retargetErr := service.restoreIngestionJobs(ctx, operation); retargetErr == nil {
				return nil
			}
		}
		if readErr == nil &&
			registered.ContainerID != operation.attempt.Previous.ContainerID &&
			(operation.candidate == nil || registered.ContainerID != operation.candidate.ContainerID) {
			return errors.New("registration matches neither rollback deployment")
		}
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("registration rollback outcome remains uncertain: %w", lastErr)
		case <-timer.C:
		}
	}
}

func (service *Service) restoreIngestionJobs(ctx context.Context, operation *upgradeOperation) error {
	if operation.candidate == nil {
		return errors.New("candidate identity is unavailable for ingestion rollback")
	}
	previousVersion := operation.attempt.Previous.ModelVersion
	if previousVersion == "" && operation.previousInfo != nil {
		previousVersion = operation.previousInfo.Version
	}
	if err := service.Repository.RetargetInferenceIngestionJobs(
		ctx,
		operation.attempt.TenantID,
		operation.candidate.ContainerID,
		operation.attempt.Previous.ContainerID,
		previousVersion,
	); err != nil {
		return fmt.Errorf("ingestion job target rollback failed: %w", err)
	}
	return nil
}

func (service *Service) verifyRestored(ctx context.Context, operation *upgradeOperation) error {
	container, err := service.Docker.GetContainerInfo(ctx, operation.attempt.Previous.ContainerID)
	if err != nil || !container.Running {
		return errors.New("restored container is not running")
	}
	name := strings.TrimPrefix(strings.TrimSpace(container.Name), "/")
	metadata, err := service.API.GetModelInfo(ctx, name)
	if err != nil || !metadata.Success {
		return errors.New("restored model metadata smoke test failed")
	}
	if operation.previousInfo != nil && (metadata.Data.ModelID != operation.previousInfo.ModelID || metadata.Data.Version != operation.previousInfo.Version) {
		return errors.New("restored model identity does not match the previous deployment")
	}
	runtime, err := service.API.GetModelRuntime(ctx, name)
	if err != nil || runtime.State != api.RuntimeReady || !runtime.Loaded || runtime.ActiveRequests != 0 {
		return errors.New("restored model lifecycle smoke test failed")
	}
	return nil
}

func (service *Service) successCommitConfirmed(ctx context.Context, operation *upgradeOperation) bool {
	registered, modelErr := service.Repository.GetInferenceModelForUpgrade(ctx, operation.attempt.TenantID, operation.attempt.ModelID)
	persisted, attemptErr := service.Repository.GetInferenceModelUpgrade(ctx, operation.attempt.TenantID, operation.attempt.ModelID, operation.attempt.ID)
	return modelErr == nil && attemptErr == nil &&
		registered.ContainerID == operation.candidate.ContainerID && registered.ActiveUpgradeID == "" &&
		persisted.State == entity.InferenceModelUpgradeSucceeded
}

func (service *Service) degrade(operation *upgradeOperation, rollbackError error) {
	operation.attempt.State = entity.InferenceModelUpgradeDegraded
	operation.attempt.ErrorCode = apiError.InferenceUpgradeDegraded
	operation.attempt.ErrorMessage = "The upgrade failed and automatic recovery requires operator intervention."
	operation.attempt.RollbackOutcome = "failed"
	operation.attempt.RollbackError = "Automatic rollback could not restore a verified deployment."
	operation.attempt.UpdatedAt = service.Now().UTC().Unix()
	operation.attempt.CompletedAt = operation.attempt.UpdatedAt
	ctx, cancel := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
	defer cancel()
	// Keep the registration lock in place. A degraded deployment must be
	// inspected and repaired before another upgrade can safely begin.
	if err := service.saveDegradedWithRetry(ctx, operation.attempt); err != nil {
		log.Printf("[model-upgrade] upgrade_id=%s state=degraded persistence_failed=%v", operation.attempt.ID, err)
		service.scheduleDegradedPersistence(operation.attempt)
	}
	service.Metrics.ObserveCompletion(operation.attempt.State, service.elapsed(operation))
	log.Printf("[model-upgrade] upgrade_id=%s state=degraded rollback_failed=%v", operation.attempt.ID, rollbackError)
}

func (service *Service) saveDegradedWithRetry(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
) error {
	var lastErr error
	for {
		lastErr = service.Repository.SaveInferenceModelUpgrade(ctx, attempt)
		if lastErr == nil {
			return nil
		}
		persisted, readErr := service.Repository.GetInferenceModelUpgrade(
			ctx, attempt.TenantID, attempt.ModelID, attempt.ID,
		)
		if readErr == nil && persisted.State == entity.InferenceModelUpgradeDegraded &&
			persisted.CompletedAt == attempt.CompletedAt {
			return nil
		}
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("degraded-state persistence retry exhausted: %w", lastErr)
		case <-timer.C:
		}
	}
}

func (service *Service) scheduleDegradedPersistence(attempt entity.InferenceModelUpgrade) {
	service.mu.Lock()
	if service.persistScheduled[attempt.ID] {
		service.mu.Unlock()
		return
	}
	service.persistScheduled[attempt.ID] = true
	service.mu.Unlock()
	delay := service.Config.PollInterval
	if delay < 30*time.Second {
		delay = 30 * time.Second
	}
	service.Schedule(delay, func() {
		service.mu.Lock()
		delete(service.persistScheduled, attempt.ID)
		service.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
		err := service.saveDegradedWithRetry(ctx, attempt)
		cancel()
		if err != nil {
			service.scheduleDegradedPersistence(attempt)
		}
	})
}

func (service *Service) scheduleRecovery(attempt entity.InferenceModelUpgrade) {
	service.mu.Lock()
	if service.recoveryScheduled[attempt.ID] {
		service.mu.Unlock()
		return
	}
	service.recoveryScheduled[attempt.ID] = true
	service.mu.Unlock()
	delay := service.Config.PollInterval
	if delay < 30*time.Second {
		delay = 30 * time.Second
	}
	service.Schedule(delay, func() {
		service.mu.Lock()
		delete(service.recoveryScheduled, attempt.ID)
		service.mu.Unlock()
		readCtx, cancelRead := context.WithTimeout(context.Background(), service.Config.ReadinessTimeout)
		persisted, attemptErr := service.Repository.GetInferenceModelUpgrade(
			readCtx, attempt.TenantID, attempt.ModelID, attempt.ID,
		)
		model, modelErr := service.Repository.GetInferenceModelForUpgrade(
			readCtx, attempt.TenantID, attempt.ModelID,
		)
		cancelRead()
		if attemptErr != nil || modelErr != nil {
			service.scheduleRecovery(attempt)
			return
		}
		recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
		service.recover(recoveryCtx, persisted, model)
		cancelRecovery()
	})
}

func (service *Service) recover(ctx context.Context, attempt entity.InferenceModelUpgrade, model entity.InferenceModel) {
	if attempt.State.Terminal() {
		service.recoverCleanup(ctx, attempt)
		return
	}
	if attempt.Candidate == nil {
		candidateContainer, err := service.Docker.GetContainerInfo(ctx, candidateContainerName(attempt.ID))
		if err == nil && candidateContainer.ID != "" && candidateContainer.ID != attempt.Previous.ContainerID {
			attempt.Candidate = &entity.InferenceModelDeployment{ContainerID: candidateContainer.ID, ImageReference: attempt.RequestedImageReference}
		}
	}
	operation := &upgradeOperation{attempt: attempt, model: model, candidate: attempt.Candidate, startedAt: service.Now()}
	if attempt.Candidate != nil && model.ContainerID == attempt.Candidate.ContainerID {
		operation.managerSwapped = true
		operation.activated = true
		service.fail(ctx, operation, errors.New("interrupted upgrade recovered after activation"))
		return
	}
	if model.ContainerID == attempt.Previous.ContainerID {
		if attempt.State == entity.InferenceModelUpgradeRollingBack && attempt.Candidate != nil {
			operation.activated = true
			if err := service.restoreIngestionJobs(ctx, operation); err != nil {
				if blockErr := service.blockAndDrainExclusive(ctx, attempt.ModelID, model.ContainerID); blockErr != nil {
					err = errors.Join(err, fmt.Errorf("restored deployment could not be blocked: %w", blockErr))
				}
				service.degrade(operation, err)
				return
			}
		}
		operation.oldBlocked = false
		service.fail(ctx, operation, errors.New("interrupted upgrade recovered before activation"))
		return
	}
	blockErr := service.blockAndDrainExclusive(ctx, attempt.ModelID, model.ContainerID)
	if blockErr != nil {
		service.degrade(operation, fmt.Errorf("unknown registered deployment could not be blocked: %w", blockErr))
		return
	}
	service.degrade(operation, errors.New("registered container matches neither upgrade identity"))
}

func (service *Service) blockAndDrainExclusive(ctx context.Context, modelID, containerID string) error {
	if err := service.ensureExclusiveContainer(ctx, modelID, containerID); err != nil {
		return err
	}
	return service.Manager.BlockAndDrain(ctx, containerID)
}

func (service *Service) ensureExclusiveContainer(ctx context.Context, modelID, containerID string) error {
	shared, err := service.Repository.HasOtherInferenceModelRegistrations(ctx, modelID, containerID)
	if err != nil {
		return fmt.Errorf("container registration exclusivity could not be verified: %w", err)
	}
	if shared {
		return errors.New("container is shared by another model registration")
	}
	return nil
}

func (service *Service) removeUnregisteredContainer(ctx context.Context, containerID string) error {
	registered, err := service.Repository.HasOtherInferenceModelRegistrations(ctx, "", containerID)
	if err != nil {
		return fmt.Errorf("container registration status could not be verified before cleanup: %w", err)
	}
	if registered {
		return errors.New("container is still referenced by a model registration")
	}
	return service.Docker.RemoveContainer(ctx, containerID)
}

func (service *Service) recoverCleanup(ctx context.Context, attempt entity.InferenceModelUpgrade) {
	if attempt.PendingCleanupContainerID == "" {
		return
	}
	active, err := service.Repository.HasActiveInferenceProcessingJobs(
		ctx, attempt.TenantID, attempt.ModelName, pendingCleanupModelVersion(attempt),
	)
	if err != nil || active {
		log.Printf("[model-upgrade] upgrade_id=%s state=%s cleanup=pending", attempt.ID, attempt.State)
		service.scheduleCleanup(attempt)
		return
	}
	if err := service.removeUnregisteredContainer(ctx, attempt.PendingCleanupContainerID); err != nil {
		log.Printf("[model-upgrade] upgrade_id=%s state=%s cleanup=pending", attempt.ID, attempt.State)
		service.scheduleCleanup(attempt)
		return
	}
	attempt.PendingCleanupContainerID = ""
	attempt.UpdatedAt = service.Now().UTC().Unix()
	if err := service.Repository.SaveInferenceModelUpgrade(ctx, attempt); err != nil {
		log.Printf("[model-upgrade] upgrade_id=%s state=%s cleanup_commit=pending", attempt.ID, attempt.State)
		service.scheduleCleanup(attempt)
	}
}

func (service *Service) scheduleCleanup(attempt entity.InferenceModelUpgrade) {
	service.mu.Lock()
	if service.cleanupScheduled[attempt.ID] {
		service.mu.Unlock()
		return
	}
	service.cleanupScheduled[attempt.ID] = true
	service.mu.Unlock()
	delay := service.Config.PollInterval
	if delay < 30*time.Second {
		delay = 30 * time.Second
	}
	service.Schedule(delay, func() {
		service.mu.Lock()
		delete(service.cleanupScheduled, attempt.ID)
		service.mu.Unlock()
		readCtx, cancelRead := context.WithTimeout(context.Background(), service.Config.ReadinessTimeout)
		persisted, err := service.Repository.GetInferenceModelUpgrade(
			readCtx, attempt.TenantID, attempt.ModelID, attempt.ID,
		)
		cancelRead()
		if err != nil {
			service.scheduleCleanup(attempt)
			return
		}
		if persisted.PendingCleanupContainerID == "" {
			return
		}
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), service.Config.OperationTimeout)
		service.recoverCleanup(cleanupCtx, persisted)
		cancelCleanup()
	})
}

func (service *Service) waitForProcessingJobs(
	ctx context.Context,
	attempt entity.InferenceModelUpgrade,
	modelVersion string,
) error {
	for {
		active, err := service.Repository.HasActiveInferenceProcessingJobs(
			ctx, attempt.TenantID, attempt.ModelName, modelVersion,
		)
		if err != nil {
			return err
		}
		if !active {
			return nil
		}
		timer := time.NewTimer(service.Config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func pendingCleanupModelVersion(attempt entity.InferenceModelUpgrade) string {
	if attempt.PendingCleanupContainerID == attempt.Previous.ContainerID {
		return attempt.Previous.ModelVersion
	}
	if attempt.Candidate != nil && attempt.PendingCleanupContainerID == attempt.Candidate.ContainerID {
		return attempt.Candidate.ModelVersion
	}
	return ""
}

func (service *Service) elapsed(operation *upgradeOperation) time.Duration {
	if operation.startedAt.IsZero() {
		return 0
	}
	return service.Now().Sub(operation.startedAt)
}

func validTransition(from, to entity.InferenceModelUpgradeState) bool {
	switch from {
	case entity.InferenceModelUpgradeQueued:
		return to == entity.InferenceModelUpgradePulling
	case entity.InferenceModelUpgradePulling:
		return to == entity.InferenceModelUpgradeValidating
	case entity.InferenceModelUpgradeValidating:
		return to == entity.InferenceModelUpgradeDraining
	case entity.InferenceModelUpgradeDraining:
		return to == entity.InferenceModelUpgradeActivating
	case entity.InferenceModelUpgradeActivating:
		return to == entity.InferenceModelUpgradeVerifying
	default:
		return false
	}
}

func (service *Service) removeRunning(id string) {
	service.mu.Lock()
	delete(service.running, id)
	service.mu.Unlock()
}

func candidateContainerName(id string) string {
	if len(id) > 12 {
		id = id[len(id)-12:]
	}
	return "pacs-ai-upgrade-" + strings.ToLower(id)
}

func failureMessage(state entity.InferenceModelUpgradeState) string {
	switch state {
	case entity.InferenceModelUpgradePulling:
		return "The candidate image could not be pulled."
	case entity.InferenceModelUpgradeValidating:
		return "The candidate image did not pass identity and readiness validation."
	case entity.InferenceModelUpgradeDraining:
		return "Active inference did not drain within the permitted timeout."
	case entity.InferenceModelUpgradeActivating, entity.InferenceModelUpgradeVerifying, entity.InferenceModelUpgradeRollingBack:
		return "The candidate could not be activated safely."
	default:
		return "The model upgrade failed safely."
	}
}
