package service

import (
	"context"
	"errors"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryService handles the Inference query service logic
type InferenceQueryService struct {
	repository.InferenceQueryRepositoryInterface
	dockerTypes.DockerSDKInterface
}

// GetContainerInfo returns the container info
func (service *InferenceQueryService) GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error) {
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return types.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}

	return types.GetContainerInfoResult{
		ID:              containerInfo.ID,
		Name:            containerInfo.Name,
		Status:          containerInfo.Status,
		Running:         containerInfo.Running,
		StartedAt:       containerInfo.StartedAt,
		FinishedAt:      containerInfo.FinishedAt,
		CPUPercentUsage: containerInfo.CPUPercentUsage,
		MemoryInBytes:   containerInfo.MemoryInBytes,
	}, nil
}
