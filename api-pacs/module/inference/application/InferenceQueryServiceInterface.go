package application

import (
	"context"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryServiceInterface holds the implementable methods for the inference query service
type InferenceQueryServiceInterface interface {
	// GetContainerInfo returns the container info with stats
	GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error)
	// GetInferenceModels returns the inference models
	GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelResult, error)
	// GetInferenceModelInfo gets the inference model info
	GetInferenceModelInfo(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelInfoResponse, error)
	// GetInferenceModelFacts gets the inference model facts
	GetInferenceModelFacts(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelFactsResponse, error)
	// GetInferenceAvailableModels gets the inference available models
	GetInferenceAvailableModels(ctx context.Context, tenantID string) ([]types.GetInferenceAvailableModelResult, error)
	// GetModelFeedBackByUser gets the model feedback by user
	GetModelFeedBackByUser(ctx context.Context, data types.GetModelFeedbackByUser) (types.GetModelFeedbackResult, error)
}
