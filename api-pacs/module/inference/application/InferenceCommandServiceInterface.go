package application

import (
	"context"

	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceCommandServiceInterface holds the implementable methods for the user command service
type InferenceCommandServiceInterface interface {
	// AddInferenceModel adds an inference model
	AddInferenceModel(ctx context.Context, data types.AddInferenceModel) error
	// DeleteInferenceModel deletes an inference model
	DeleteInferenceModel(ctx context.Context, ID string) error
	// UpdateInferenceModel updates an inference model
	UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error
	// UpdateInferenceModelContainerID updates the container ID of an inference model
	UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error
}
