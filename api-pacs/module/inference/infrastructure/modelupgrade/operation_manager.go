package modelupgrade

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/segmentio/ksuid"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
)

type DeploymentExecutor interface {
	Upgrade(context.Context, UpgradeRequest) (UpgradeResult, error)
}

type StartDeploymentOperation struct {
	TenantID    string
	ModelID     string
	ActorUserID string
	Source      entity.InferenceModelDeployment
	Target      entity.InferenceModelVersion
}

type OperationManagerConfig struct {
	OperationTimeout   time.Duration
	PersistenceTimeout time.Duration
}

type OperationManager struct {
	repository repository.InferenceModelDeploymentOperationRepositoryInterface
	executor   DeploymentExecutor
	config     OperationManagerConfig
	now        func() time.Time
	newID      func() string
	launch     func(func())
}

func NewOperationManager(
	repository repository.InferenceModelDeploymentOperationRepositoryInterface,
	executor DeploymentExecutor,
	config OperationManagerConfig,
) (*OperationManager, error) {
	if repository == nil || executor == nil {
		return nil, errors.New("model deployment operation dependencies are required")
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = 30 * time.Minute
	}
	if config.PersistenceTimeout <= 0 {
		config.PersistenceTimeout = 15 * time.Second
	}
	return &OperationManager{
		repository: repository,
		executor:   executor,
		config:     config,
		now:        time.Now,
		newID:      func() string { return ksuid.New().String() },
		launch:     func(run func()) { go run() },
	}, nil
}

func (manager *OperationManager) Start(
	ctx context.Context,
	request StartDeploymentOperation,
) (entity.InferenceModelDeploymentOperation, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ModelID = strings.TrimSpace(request.ModelID)
	request.ActorUserID = strings.TrimSpace(request.ActorUserID)
	request.Target.Normalize()
	now := manager.now().UnixMilli()
	operation := entity.InferenceModelDeploymentOperation{
		ID: manager.newID(), TenantID: request.TenantID, ModelID: request.ModelID, ActorUserID: request.ActorUserID,
		CatalogVersionID: request.Target.ID, RequestedVersion: request.Target.Version,
		RequestedImage: request.Target.ImageReference, RequestedImageDigest: request.Target.ImageDigest,
		Source: request.Source, Status: entity.InferenceModelDeploymentOperationPending,
		Stage: entity.InferenceModelDeploymentStageResolveActive, Sequence: 1, CreatedAt: now, UpdatedAt: now,
	}
	operation.Normalize()
	if err := operation.Validate(); err != nil {
		return entity.InferenceModelDeploymentOperation{}, errors.New(apiError.InvalidPayload)
	}
	if err := manager.repository.CreateInferenceModelDeploymentOperation(ctx, operation); err != nil {
		return entity.InferenceModelDeploymentOperation{}, err
	}
	log.Printf(
		"[model-deployment] event=accepted deployment_id=%s tenant_id=%s model_id=%s version_id=%s actor_user_id=%s",
		operation.ID, operation.TenantID, operation.ModelID, operation.CatalogVersionID, operation.ActorUserID,
	)
	manager.launch(func() { manager.run(operation) })
	return operation, nil
}

func (manager *OperationManager) Get(
	ctx context.Context,
	tenantID, modelID, operationID string,
) (entity.InferenceModelDeploymentOperation, error) {
	tenantID = strings.TrimSpace(tenantID)
	modelID = strings.TrimSpace(modelID)
	operationID = strings.TrimSpace(operationID)
	if tenantID == "" || modelID == "" || operationID == "" {
		return entity.InferenceModelDeploymentOperation{}, errors.New(apiError.InvalidPayload)
	}
	operation, err := manager.repository.SelectInferenceModelDeploymentOperation(ctx, operationID)
	if err != nil {
		return entity.InferenceModelDeploymentOperation{}, err
	}
	if operation.TenantID != tenantID || operation.ModelID != modelID {
		return entity.InferenceModelDeploymentOperation{}, errors.New(apiError.MissingRecord)
	}
	return operation, nil
}

// ReconcileInterrupted closes operations left non-terminal by the previous API
// process. The coordinator cannot safely resume an unknown Docker stage.
func (manager *OperationManager) ReconcileInterrupted(ctx context.Context) error {
	operations, err := manager.repository.ListNonTerminalInferenceModelDeploymentOperations(ctx)
	if err != nil {
		return err
	}
	var reconciliationErrors []error
	for _, operation := range operations {
		now := manager.now().UnixMilli()
		operation.Status = entity.InferenceModelDeploymentOperationInterrupted
		operation.ErrorCode = apiError.InferenceDeploymentInterrupted
		operation.ErrorMessage = "The API restarted before the deployment reached a terminal state. Verify the active model before retrying."
		operation.Sequence++
		operation.UpdatedAt = now
		operation.FinishedAt = now
		if updateErr := manager.repository.UpdateInferenceModelDeploymentOperation(ctx, operation); updateErr != nil {
			reconciliationErrors = append(reconciliationErrors, fmt.Errorf("deployment %s: %w", operation.ID, updateErr))
			continue
		}
		log.Printf(
			"[model-deployment] event=interrupted deployment_id=%s tenant_id=%s model_id=%s",
			operation.ID, operation.TenantID, operation.ModelID,
		)
	}
	return errors.Join(reconciliationErrors...)
}

func (manager *OperationManager) run(operation entity.InferenceModelDeploymentOperation) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf(
				"[model-deployment] event=panic deployment_id=%s panic_type=%T stack=%s",
				operation.ID, recovered, debug.Stack(),
			)
			manager.finishFailure(&operation, &UpgradeError{
				Stage: operation.Stage, Err: errors.New(apiError.ServerError),
			})
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), manager.config.OperationTimeout)
	defer cancel()
	result, err := manager.executor.Upgrade(ctx, UpgradeRequest{
		TenantID: operation.TenantID, ModelID: operation.ModelID, VersionID: operation.CatalogVersionID,
		OnStage: func(_ context.Context, stage UpgradeStage) error {
			now := manager.now().UnixMilli()
			next := operation
			next.Status = entity.InferenceModelDeploymentOperationRunning
			next.Stage = stage
			next.Sequence++
			if next.StartedAt == 0 {
				next.StartedAt = now
			}
			next.UpdatedAt = now
			if persistErr := manager.persist(next); persistErr != nil {
				return persistErr
			}
			operation = next
			return nil
		},
	})
	if err != nil {
		manager.finishFailure(&operation, err)
		return
	}
	now := manager.now().UnixMilli()
	operation.Status = entity.InferenceModelDeploymentOperationCompleted
	operation.Active = &result.Active
	operation.ContainerName = result.ContainerName
	operation.Sequence++
	operation.UpdatedAt = now
	operation.FinishedAt = now
	if err = manager.persist(operation); err != nil {
		log.Printf("[model-deployment] event=completion_persist_failed deployment_id=%s err=%v", operation.ID, err)
		return
	}
	log.Printf(
		"[model-deployment] event=completed deployment_id=%s tenant_id=%s model_id=%s version_id=%s",
		operation.ID, operation.TenantID, operation.ModelID, operation.CatalogVersionID,
	)
}

func (manager *OperationManager) finishFailure(operation *entity.InferenceModelDeploymentOperation, err error) {
	stage := operation.Stage
	var upgradeErr *UpgradeError
	if errors.As(err, &upgradeErr) {
		stage = upgradeErr.Stage
	}
	now := manager.now().UnixMilli()
	operation.Stage = stage
	operation.Status = entity.InferenceModelDeploymentOperationFailed
	operation.ErrorCode = deploymentPublicErrorCode(err)
	operation.ErrorMessage = deploymentPublicErrorMessage(stage)
	operation.RollbackError = ""
	if errors.As(err, &upgradeErr) && upgradeErr.RollbackAttempted {
		if upgradeErr.RollbackErr == nil {
			operation.Status = entity.InferenceModelDeploymentOperationRolledBack
		} else {
			operation.RollbackError = "The previous deployment could not be fully restored automatically."
		}
	}
	operation.Sequence++
	operation.UpdatedAt = now
	operation.FinishedAt = now
	if persistErr := manager.persist(*operation); persistErr != nil {
		log.Printf("[model-deployment] event=failure_persist_failed deployment_id=%s err=%v", operation.ID, persistErr)
		return
	}
	log.Printf(
		"[model-deployment] event=failed deployment_id=%s tenant_id=%s model_id=%s stage=%s status=%s error_code=%s rollback_error=%t",
		operation.ID, operation.TenantID, operation.ModelID, stage, operation.Status, operation.ErrorCode,
		operation.RollbackError != "",
	)
}

func (manager *OperationManager) persist(operation entity.InferenceModelDeploymentOperation) error {
	ctx, cancel := context.WithTimeout(context.Background(), manager.config.PersistenceTimeout)
	defer cancel()
	return manager.repository.UpdateInferenceModelDeploymentOperation(ctx, operation)
}

func deploymentPublicErrorCode(err error) string {
	var upgradeErr *UpgradeError
	if errors.As(err, &upgradeErr) && upgradeErr.Err != nil {
		switch upgradeErr.Err.Error() {
		case apiError.InvalidPayload,
			apiError.MissingRecord,
			apiError.InferenceUpgradeConflict,
			apiError.InferenceUpgradeUnavailable,
			apiError.InferenceCandidateDeploymentDisabled:
			return upgradeErr.Err.Error()
		}
	}
	return apiError.InferenceDeploymentFailed
}

func deploymentPublicErrorMessage(stage UpgradeStage) string {
	switch stage {
	case UpgradeStageResolveActive:
		return "The active model deployment could not be resolved."
	case UpgradeStageResolveVersion:
		return "The selected model version could not be resolved."
	case UpgradeStagePull:
		return "The selected model image could not be pulled or verified."
	case UpgradeStageCreate:
		return "The candidate model container could not be created."
	case UpgradeStageValidate:
		return "The candidate model container failed validation."
	case UpgradeStageDrain:
		return "Active inference requests could not be drained safely."
	case UpgradeStagePrepare:
		return "The candidate model could not be prepared for activation."
	case UpgradeStageActivate:
		return "The candidate model could not be activated."
	default:
		return "The model deployment failed."
	}
}
