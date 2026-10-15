package entity

import (
	"fmt"
	"strings"
)

type InferenceModelDeploymentOperationStatus string
type InferenceModelDeploymentOperationStage string

const (
	InferenceModelDeploymentOperationPending     InferenceModelDeploymentOperationStatus = "PENDING"
	InferenceModelDeploymentOperationRunning     InferenceModelDeploymentOperationStatus = "RUNNING"
	InferenceModelDeploymentOperationCompleted   InferenceModelDeploymentOperationStatus = "COMPLETED"
	InferenceModelDeploymentOperationFailed      InferenceModelDeploymentOperationStatus = "FAILED"
	InferenceModelDeploymentOperationRolledBack  InferenceModelDeploymentOperationStatus = "ROLLED_BACK"
	InferenceModelDeploymentOperationInterrupted InferenceModelDeploymentOperationStatus = "INTERRUPTED"

	InferenceModelDeploymentStageResolveActive  InferenceModelDeploymentOperationStage = "resolve_active"
	InferenceModelDeploymentStageResolveVersion InferenceModelDeploymentOperationStage = "resolve_version"
	InferenceModelDeploymentStagePull           InferenceModelDeploymentOperationStage = "pull"
	InferenceModelDeploymentStageCreate         InferenceModelDeploymentOperationStage = "create_candidate"
	InferenceModelDeploymentStageValidate       InferenceModelDeploymentOperationStage = "validate_candidate"
	InferenceModelDeploymentStageDrain          InferenceModelDeploymentOperationStage = "drain_active"
	InferenceModelDeploymentStagePrepare        InferenceModelDeploymentOperationStage = "prepare_candidate"
	InferenceModelDeploymentStageActivate       InferenceModelDeploymentOperationStage = "activate_candidate"
)

// InferenceModelDeploymentOperation is the durable state of one administrator-
// initiated model deployment. It is also the source of truth for live progress.
type InferenceModelDeploymentOperation struct {
	ID                   string                                  `firestore:"id,omitempty" json:"id"`
	TenantID             string                                  `firestore:"tenant_id" json:"tenantId"`
	ModelID              string                                  `firestore:"model_id" json:"modelId"`
	ActorUserID          string                                  `firestore:"actor_user_id" json:"actorUserId"`
	CatalogVersionID     string                                  `firestore:"catalog_version_id" json:"catalogVersionId"`
	RequestedVersion     string                                  `firestore:"requested_version" json:"requestedVersion"`
	RequestedImage       string                                  `firestore:"requested_image" json:"requestedImage"`
	RequestedImageDigest string                                  `firestore:"requested_image_digest" json:"requestedImageDigest"`
	Source               InferenceModelDeployment                `firestore:"source" json:"source"`
	Active               *InferenceModelDeployment               `firestore:"active,omitempty" json:"active,omitempty"`
	ContainerName        string                                  `firestore:"container_name,omitempty" json:"containerName,omitempty"`
	Status               InferenceModelDeploymentOperationStatus `firestore:"status" json:"status"`
	Stage                InferenceModelDeploymentOperationStage  `firestore:"stage" json:"stage"`
	Sequence             int64                                   `firestore:"sequence" json:"sequence"`
	ErrorCode            string                                  `firestore:"error_code,omitempty" json:"errorCode,omitempty"`
	ErrorMessage         string                                  `firestore:"error_message,omitempty" json:"errorMessage,omitempty"`
	RollbackError        string                                  `firestore:"rollback_error,omitempty" json:"rollbackError,omitempty"`
	CreatedAt            int64                                   `firestore:"created_at" json:"createdAt"`
	StartedAt            int64                                   `firestore:"started_at,omitempty" json:"startedAt,omitempty"`
	UpdatedAt            int64                                   `firestore:"updated_at" json:"updatedAt"`
	FinishedAt           int64                                   `firestore:"finished_at,omitempty" json:"finishedAt,omitempty"`
}

func (*InferenceModelDeploymentOperation) GetModelName() string {
	return "inference_model_deployment_operations"
}

func (operation *InferenceModelDeploymentOperation) Normalize() {
	operation.ID = strings.TrimSpace(operation.ID)
	operation.TenantID = strings.TrimSpace(operation.TenantID)
	operation.ModelID = strings.TrimSpace(operation.ModelID)
	operation.ActorUserID = strings.TrimSpace(operation.ActorUserID)
	operation.CatalogVersionID = strings.TrimSpace(operation.CatalogVersionID)
	operation.RequestedVersion = strings.TrimSpace(operation.RequestedVersion)
	operation.RequestedImage = strings.TrimSpace(operation.RequestedImage)
	operation.RequestedImageDigest = strings.TrimSpace(operation.RequestedImageDigest)
	operation.ContainerName = strings.TrimSpace(operation.ContainerName)
	operation.Status = InferenceModelDeploymentOperationStatus(strings.ToUpper(strings.TrimSpace(string(operation.Status))))
	operation.Stage = InferenceModelDeploymentOperationStage(strings.TrimSpace(string(operation.Stage)))
	operation.ErrorCode = strings.TrimSpace(operation.ErrorCode)
	operation.ErrorMessage = strings.TrimSpace(operation.ErrorMessage)
	operation.RollbackError = strings.TrimSpace(operation.RollbackError)
}

func (operation InferenceModelDeploymentOperation) Terminal() bool {
	switch operation.Status {
	case InferenceModelDeploymentOperationCompleted,
		InferenceModelDeploymentOperationFailed,
		InferenceModelDeploymentOperationRolledBack,
		InferenceModelDeploymentOperationInterrupted:
		return true
	default:
		return false
	}
}

func (operation InferenceModelDeploymentOperation) Validate() error {
	if operation.ID == "" || len(operation.ID) > 128 || operation.TenantID == "" || operation.ModelID == "" ||
		operation.ActorUserID == "" || operation.CatalogVersionID == "" || operation.RequestedVersion == "" ||
		operation.RequestedImage == "" || operation.RequestedImageDigest == "" {
		return fmt.Errorf("deployment operation identity is incomplete")
	}
	if operation.Source.ContainerID == "" || operation.Source.ImageReference == "" {
		return fmt.Errorf("deployment operation source is incomplete")
	}
	if !validInferenceModelDeploymentStage(operation.Stage) {
		return fmt.Errorf("deployment operation stage is invalid")
	}
	if operation.Sequence <= 0 || operation.CreatedAt <= 0 || operation.UpdatedAt < operation.CreatedAt {
		return fmt.Errorf("deployment operation timing is invalid")
	}
	switch operation.Status {
	case InferenceModelDeploymentOperationPending:
		if operation.StartedAt != 0 || operation.FinishedAt != 0 {
			return fmt.Errorf("pending deployment operation cannot be started or finished")
		}
	case InferenceModelDeploymentOperationRunning:
		if operation.StartedAt < operation.CreatedAt || operation.FinishedAt != 0 {
			return fmt.Errorf("running deployment operation timing is invalid")
		}
	case InferenceModelDeploymentOperationCompleted:
		if operation.StartedAt < operation.CreatedAt || operation.FinishedAt < operation.StartedAt || operation.Active == nil {
			return fmt.Errorf("completed deployment operation is incomplete")
		}
	case InferenceModelDeploymentOperationFailed,
		InferenceModelDeploymentOperationRolledBack,
		InferenceModelDeploymentOperationInterrupted:
		if operation.FinishedAt < operation.CreatedAt ||
			(operation.StartedAt > 0 && operation.FinishedAt < operation.StartedAt) ||
			operation.ErrorCode == "" || operation.ErrorMessage == "" {
			return fmt.Errorf("terminal deployment operation failure is incomplete")
		}
	default:
		return fmt.Errorf("deployment operation status is invalid")
	}
	return nil
}

func validInferenceModelDeploymentStage(stage InferenceModelDeploymentOperationStage) bool {
	switch stage {
	case InferenceModelDeploymentStageResolveActive,
		InferenceModelDeploymentStageResolveVersion,
		InferenceModelDeploymentStagePull,
		InferenceModelDeploymentStageCreate,
		InferenceModelDeploymentStageValidate,
		InferenceModelDeploymentStageDrain,
		InferenceModelDeploymentStagePrepare,
		InferenceModelDeploymentStageActivate:
		return true
	default:
		return false
	}
}
