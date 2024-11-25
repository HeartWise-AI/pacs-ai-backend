package application

import (
	"context"

	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryServiceInterface holds the implementable methods for the inference query service
type InferenceQueryServiceInterface interface {
	// GetContainerInfo returns the container info
	GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error)
}
