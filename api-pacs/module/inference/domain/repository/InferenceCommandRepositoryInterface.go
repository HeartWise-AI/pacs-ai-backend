package repository

import (
	"context"

	"api-pacs/module/inference/infrastructure/repository/types"
)

type InferenceCommandRepositoryInterface interface {
	// DeleteInferenceModel deletes an inference model
	DeleteInferenceModel(ctx context.Context, ID string) error
	// InsertInferenceModel inserts an inference model
	InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error
	// UpdateInferenceModel updates an inference model
	UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error
	// UpdateInferenceModelContainerID updates the container ID of an inference model
	UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error
}
