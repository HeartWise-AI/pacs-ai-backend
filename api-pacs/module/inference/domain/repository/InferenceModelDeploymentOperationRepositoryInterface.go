package repository

import (
	"context"

	"api-pacs/module/inference/domain/entity"
)

type InferenceModelDeploymentOperationRepositoryInterface interface {
	CreateInferenceModelDeploymentOperation(context.Context, entity.InferenceModelDeploymentOperation) error
	UpdateInferenceModelDeploymentOperation(context.Context, entity.InferenceModelDeploymentOperation) error
	SelectInferenceModelDeploymentOperation(context.Context, string) (entity.InferenceModelDeploymentOperation, error)
	ListNonTerminalInferenceModelDeploymentOperations(context.Context) ([]entity.InferenceModelDeploymentOperation, error)
}
