package modelupgrade

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

type operationTestRepository struct {
	mu     sync.Mutex
	items  map[string]entity.InferenceModelDeploymentOperation
	active map[string]string
}

func newOperationTestRepository() *operationTestRepository {
	return &operationTestRepository{
		items:  make(map[string]entity.InferenceModelDeploymentOperation),
		active: make(map[string]string),
	}
}

func operationTestKey(tenantID, modelID string) string { return tenantID + "\x00" + modelID }

func (repository *operationTestRepository) CreateInferenceModelDeploymentOperation(
	_ context.Context,
	operation entity.InferenceModelDeploymentOperation,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := operationTestKey(operation.TenantID, operation.ModelID)
	if _, exists := repository.active[key]; exists {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	repository.items[operation.ID] = operation
	repository.active[key] = operation.ID
	return nil
}

func (repository *operationTestRepository) UpdateInferenceModelDeploymentOperation(
	_ context.Context,
	operation entity.InferenceModelDeploymentOperation,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.items[operation.ID]
	if !exists {
		return errors.New(apiError.MissingRecord)
	}
	if operation.Sequence != current.Sequence+1 {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	repository.items[operation.ID] = operation
	if operation.Terminal() {
		delete(repository.active, operationTestKey(operation.TenantID, operation.ModelID))
	}
	return nil
}

func (repository *operationTestRepository) SelectInferenceModelDeploymentOperation(
	_ context.Context,
	id string,
) (entity.InferenceModelDeploymentOperation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	operation, exists := repository.items[id]
	if !exists {
		return entity.InferenceModelDeploymentOperation{}, errors.New(apiError.MissingRecord)
	}
	return operation, nil
}

func (repository *operationTestRepository) ListNonTerminalInferenceModelDeploymentOperations(
	context.Context,
) ([]entity.InferenceModelDeploymentOperation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	operations := make([]entity.InferenceModelDeploymentOperation, 0)
	for _, operation := range repository.items {
		if !operation.Terminal() {
			operations = append(operations, operation)
		}
	}
	return operations, nil
}

func (repository *operationTestRepository) snapshot(id string) entity.InferenceModelDeploymentOperation {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.items[id]
}

type operationTestExecutor func(context.Context, UpgradeRequest) (UpgradeResult, error)

func (executor operationTestExecutor) Upgrade(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
	return executor(ctx, request)
}

func operationTestVersion() entity.InferenceModelVersion {
	sourceRevision := strings.Repeat("a", 40)
	return entity.InferenceModelVersion{
		ID: "catalog-version", ModelID: "artifact-model", Version: "2.0.0",
		ImageReference: "heartwisehub/model:2.0.0", ImageDigest: "sha256:" + strings.Repeat("b", 64),
		Status: entity.InferenceModelVersionAvailable,
		Provenance: &entity.InferenceModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend", SourceRevision: &sourceRevision,
			ModelRepository: "HeartWise-AI/model", ModelRevision: strings.Repeat("c", 40),
			WeightsPath: "weights/model.pt", WeightsSHA256: strings.Repeat("d", 64),
		},
	}
}

func operationTestRequest() StartDeploymentOperation {
	return StartDeploymentOperation{
		TenantID: "tenant-1", ModelID: "registration-1", ActorUserID: "user-1",
		Source: entity.InferenceModelDeployment{ContainerID: "old-container", ImageReference: "heartwisehub/model:1.0.0"},
		Target: operationTestVersion(),
	}
}

func newControlledOperationManager(
	t *testing.T,
	repository *operationTestRepository,
	executor DeploymentExecutor,
) (*OperationManager, *func()) {
	t.Helper()
	manager, err := NewOperationManager(repository, executor, OperationManagerConfig{
		OperationTimeout: time.Minute, PersistenceTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return time.UnixMilli(1000) }
	manager.newID = func() string { return "operation-1" }
	var queued func()
	manager.launch = func(run func()) { queued = run }
	return manager, &queued
}

func TestOperationManagerDetachesFromRequestAndPersistsEveryStage(t *testing.T) {
	repository := newOperationTestRepository()
	executor := operationTestExecutor(func(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
		if ctx.Err() != nil {
			t.Fatalf("background deployment inherited caller cancellation: %v", ctx.Err())
		}
		stages := []UpgradeStage{
			UpgradeStageResolveActive, UpgradeStageResolveVersion, UpgradeStagePull, UpgradeStageCreate,
			UpgradeStageValidate, UpgradeStageDrain, UpgradeStagePrepare, UpgradeStageActivate,
		}
		for _, stage := range stages {
			if err := request.OnStage(ctx, stage); err != nil {
				return UpgradeResult{}, err
			}
		}
		return UpgradeResult{
			Active: entity.InferenceModelDeployment{
				ContainerID: "new-container", ImageReference: "heartwisehub/model:2.0.0",
			},
			ContainerName: "model-candidate",
		}, nil
	})
	manager, queued := newControlledOperationManager(t, repository, executor)
	callerCtx, cancelCaller := context.WithCancel(context.Background())
	operation, err := manager.Start(callerCtx, operationTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	if *queued == nil {
		t.Fatal("deployment was not scheduled")
	}
	(*queued)()

	completed := repository.snapshot(operation.ID)
	if completed.Status != entity.InferenceModelDeploymentOperationCompleted || completed.Stage != UpgradeStageActivate ||
		completed.Sequence != 10 || completed.Active == nil || completed.Active.ContainerID != "new-container" {
		t.Fatalf("completed operation = %+v", completed)
	}
}

func TestOperationManagerRejectsConcurrentDeploymentForSameModel(t *testing.T) {
	repository := newOperationTestRepository()
	manager, _ := newControlledOperationManager(t, repository, operationTestExecutor(func(context.Context, UpgradeRequest) (UpgradeResult, error) {
		return UpgradeResult{}, nil
	}))
	if _, err := manager.Start(context.Background(), operationTestRequest()); err != nil {
		t.Fatal(err)
	}
	manager.newID = func() string { return "operation-2" }
	if _, err := manager.Start(context.Background(), operationTestRequest()); err == nil || err.Error() != apiError.InferenceUpgradeConflict {
		t.Fatalf("concurrent deployment returned %v", err)
	}
}

func TestOperationManagerRecordsSafeRollbackFailureState(t *testing.T) {
	repository := newOperationTestRepository()
	executor := operationTestExecutor(func(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
		if err := request.OnStage(ctx, UpgradeStageResolveActive); err != nil {
			return UpgradeResult{}, err
		}
		if err := request.OnStage(ctx, UpgradeStageDrain); err != nil {
			return UpgradeResult{}, err
		}
		return UpgradeResult{}, &UpgradeError{
			Stage: UpgradeStageDrain, Err: errors.New("registry secret=do-not-expose"), RollbackAttempted: true,
		}
	})
	manager, queued := newControlledOperationManager(t, repository, executor)
	operation, err := manager.Start(context.Background(), operationTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	(*queued)()
	failed := repository.snapshot(operation.ID)
	if failed.Status != entity.InferenceModelDeploymentOperationRolledBack || failed.ErrorCode != apiError.InferenceDeploymentFailed {
		t.Fatalf("failed operation = %+v", failed)
	}
	if strings.Contains(failed.ErrorMessage, "secret") || failed.RollbackError != "" {
		t.Fatalf("unsafe failure details = %+v", failed)
	}
}

func TestOperationManagerReconcilesInterruptedOperationsAndReleasesConflict(t *testing.T) {
	repository := newOperationTestRepository()
	manager, _ := newControlledOperationManager(t, repository, operationTestExecutor(func(context.Context, UpgradeRequest) (UpgradeResult, error) {
		return UpgradeResult{}, nil
	}))
	operation, err := manager.Start(context.Background(), operationTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ReconcileInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	interrupted := repository.snapshot(operation.ID)
	if interrupted.Status != entity.InferenceModelDeploymentOperationInterrupted ||
		interrupted.ErrorCode != apiError.InferenceDeploymentInterrupted || !interrupted.Terminal() {
		t.Fatalf("interrupted operation = %+v", interrupted)
	}
	manager.newID = func() string { return "operation-2" }
	if _, err = manager.Start(context.Background(), operationTestRequest()); err != nil {
		t.Fatalf("reconciled lock remained active: %v", err)
	}
}

func TestOperationManagerGetEnforcesTenantAndModelScope(t *testing.T) {
	repository := newOperationTestRepository()
	manager, _ := newControlledOperationManager(t, repository, operationTestExecutor(func(context.Context, UpgradeRequest) (UpgradeResult, error) {
		return UpgradeResult{}, nil
	}))
	operation, err := manager.Start(context.Background(), operationTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Get(context.Background(), "tenant-1", "registration-1", operation.ID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{"tenant-2", "registration-1"}, {"tenant-1", "registration-2"}} {
		if _, err = manager.Get(context.Background(), scope[0], scope[1], operation.ID); err == nil || err.Error() != apiError.MissingRecord {
			t.Fatalf("cross-scope lookup returned %v", err)
		}
	}
}
