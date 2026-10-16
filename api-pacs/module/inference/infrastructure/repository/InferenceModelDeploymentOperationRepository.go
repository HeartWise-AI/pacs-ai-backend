package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"reflect"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

const inferenceModelDeploymentLockCollection = "inference_model_deployment_locks"

type inferenceModelDeploymentLock struct {
	OperationID string `firestore:"operation_id"`
	TenantID    string `firestore:"tenant_id"`
	ModelID     string `firestore:"model_id"`
	CreatedAt   int64  `firestore:"created_at"`
}

type InferenceModelDeploymentOperationRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

func (repository *InferenceModelDeploymentOperationRepository) CreateInferenceModelDeploymentOperation(
	ctx context.Context,
	operation entity.InferenceModelDeploymentOperation,
) error {
	operation.Normalize()
	if err := operation.Validate(); err != nil || operation.Status != entity.InferenceModelDeploymentOperationPending {
		return errors.New(apiError.InvalidPayload)
	}
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}
	defer client.Close()

	operationRef := client.Collection(operation.GetModelName()).Doc(operation.ID)
	lockRef := client.Collection(inferenceModelDeploymentLockCollection).Doc(deploymentOperationLockID(operation.TenantID, operation.ModelID))
	err = client.RunTransaction(ctx, func(ctx context.Context, transaction *firestore.Transaction) error {
		if _, getErr := transaction.Get(lockRef); status.Code(getErr) != codes.NotFound {
			if getErr != nil {
				return getErr
			}
			return errors.New(apiError.InferenceUpgradeConflict)
		}
		if createErr := transaction.Create(operationRef, operation); createErr != nil {
			return createErr
		}
		return transaction.Create(lockRef, inferenceModelDeploymentLock{
			OperationID: operation.ID, TenantID: operation.TenantID, ModelID: operation.ModelID, CreatedAt: operation.CreatedAt,
		})
	})
	if err != nil {
		if err.Error() == apiError.InferenceUpgradeConflict {
			return err
		}
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}
	return nil
}

func (repository *InferenceModelDeploymentOperationRepository) UpdateInferenceModelDeploymentOperation(
	ctx context.Context,
	operation entity.InferenceModelDeploymentOperation,
) error {
	operation.Normalize()
	if err := operation.Validate(); err != nil {
		return errors.New(apiError.InvalidPayload)
	}
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}
	defer client.Close()

	operationRef := client.Collection(operation.GetModelName()).Doc(operation.ID)
	lockRef := client.Collection(inferenceModelDeploymentLockCollection).Doc(deploymentOperationLockID(operation.TenantID, operation.ModelID))
	err = client.RunTransaction(ctx, func(ctx context.Context, transaction *firestore.Transaction) error {
		snapshot, getErr := transaction.Get(operationRef)
		if status.Code(getErr) == codes.NotFound {
			return errors.New(apiError.MissingRecord)
		}
		if getErr != nil {
			return getErr
		}
		var current entity.InferenceModelDeploymentOperation
		if decodeErr := snapshot.DataTo(&current); decodeErr != nil {
			return decodeErr
		}
		current.ID = snapshot.Ref.ID
		current.Normalize()
		if transitionErr := validateDeploymentOperationUpdate(current, operation); transitionErr != nil {
			return transitionErr
		}

		lockSnapshot, lockErr := transaction.Get(lockRef)
		if lockErr != nil {
			return errors.New(apiError.InferenceUpgradeConflict)
		}
		var lock inferenceModelDeploymentLock
		if decodeErr := lockSnapshot.DataTo(&lock); decodeErr != nil || lock.OperationID != operation.ID ||
			lock.TenantID != operation.TenantID || lock.ModelID != operation.ModelID {
			return errors.New(apiError.InferenceUpgradeConflict)
		}
		if setErr := transaction.Set(operationRef, operation); setErr != nil {
			return setErr
		}
		if operation.Terminal() {
			return transaction.Delete(lockRef)
		}
		return nil
	})
	if err != nil {
		switch err.Error() {
		case apiError.InvalidPayload, apiError.MissingRecord, apiError.InferenceUpgradeConflict:
			return err
		default:
			log.Println(err)
			return errors.New(apiError.FirestoreError)
		}
	}
	return nil
}

func (repository *InferenceModelDeploymentOperationRepository) SelectInferenceModelDeploymentOperation(
	ctx context.Context,
	id string,
) (entity.InferenceModelDeploymentOperation, error) {
	var operation entity.InferenceModelDeploymentOperation
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		return operation, errors.New(apiError.FirestoreError)
	}
	defer client.Close()
	snapshot, err := client.Collection(operation.GetModelName()).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return operation, errors.New(apiError.MissingRecord)
	}
	if err != nil {
		log.Println(err)
		return operation, errors.New(apiError.FirestoreError)
	}
	if err = snapshot.DataTo(&operation); err != nil {
		return entity.InferenceModelDeploymentOperation{}, errors.New(apiError.FirestoreError)
	}
	operation.ID = snapshot.Ref.ID
	operation.Normalize()
	if err = operation.Validate(); err != nil {
		return entity.InferenceModelDeploymentOperation{}, errors.New(apiError.InvalidPayload)
	}
	return operation, nil
}

func (repository *InferenceModelDeploymentOperationRepository) ListNonTerminalInferenceModelDeploymentOperations(
	ctx context.Context,
) ([]entity.InferenceModelDeploymentOperation, error) {
	var model entity.InferenceModelDeploymentOperation
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	defer client.Close()
	documents, err := client.Collection(model.GetModelName()).Where("status", "in", []string{
		string(entity.InferenceModelDeploymentOperationPending),
		string(entity.InferenceModelDeploymentOperationRunning),
	}).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}
	operations := make([]entity.InferenceModelDeploymentOperation, 0)
	for _, document := range documents {
		var operation entity.InferenceModelDeploymentOperation
		if decodeErr := document.DataTo(&operation); decodeErr != nil {
			return nil, errors.New(apiError.FirestoreError)
		}
		operation.ID = document.Ref.ID
		operation.Normalize()
		if validationErr := operation.Validate(); validationErr != nil {
			return nil, errors.New(apiError.InvalidPayload)
		}
		operations = append(operations, operation)
	}
	return operations, nil
}

func deploymentOperationLockID(tenantID, modelID string) string {
	digest := sha256.Sum256([]byte(tenantID + "\x00" + modelID))
	return hex.EncodeToString(digest[:])
}

func validateDeploymentOperationUpdate(
	current entity.InferenceModelDeploymentOperation,
	next entity.InferenceModelDeploymentOperation,
) error {
	if current.Terminal() || current.ID != next.ID || current.TenantID != next.TenantID || current.ModelID != next.ModelID ||
		current.ActorUserID != next.ActorUserID || current.CatalogVersionID != next.CatalogVersionID ||
		current.RequestedVersion != next.RequestedVersion || current.RequestedImage != next.RequestedImage ||
		current.RequestedImageDigest != next.RequestedImageDigest || !reflect.DeepEqual(current.Source, next.Source) ||
		next.Sequence != current.Sequence+1 || next.CreatedAt != current.CreatedAt || next.UpdatedAt < current.UpdatedAt {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	if current.StartedAt > 0 && next.StartedAt != current.StartedAt {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	if current.Status == entity.InferenceModelDeploymentOperationPending {
		if next.Status != entity.InferenceModelDeploymentOperationRunning && !next.Terminal() {
			return errors.New(apiError.InferenceUpgradeConflict)
		}
	} else if current.Status == entity.InferenceModelDeploymentOperationRunning &&
		next.Status != entity.InferenceModelDeploymentOperationRunning && !next.Terminal() {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	if deploymentStageOrder(next.Stage) < deploymentStageOrder(current.Stage) {
		return errors.New(apiError.InferenceUpgradeConflict)
	}
	return nil
}

func deploymentStageOrder(stage entity.InferenceModelDeploymentOperationStage) int {
	switch stage {
	case entity.InferenceModelDeploymentStageResolveActive:
		return 1
	case entity.InferenceModelDeploymentStageResolveVersion:
		return 2
	case entity.InferenceModelDeploymentStagePull:
		return 3
	case entity.InferenceModelDeploymentStageCreate:
		return 4
	case entity.InferenceModelDeploymentStageValidate:
		return 5
	case entity.InferenceModelDeploymentStageDrain:
		return 6
	case entity.InferenceModelDeploymentStagePrepare:
		return 7
	case entity.InferenceModelDeploymentStageActivate:
		return 8
	default:
		return 0
	}
}
