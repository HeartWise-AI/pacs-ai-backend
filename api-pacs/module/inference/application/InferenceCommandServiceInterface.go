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
	// RestartInferenceModelContainer restarts an inference model container
	RestartInferenceModelContainer(ctx context.Context, containerID string) error
	// StartInferenceModelContainer starts an inference model container
	StartInferenceModelContainer(ctx context.Context, containerID string) error
	// StopInferenceModelContainer stops an inference model container
	StopInferenceModelContainer(ctx context.Context, containerID string) error
}
