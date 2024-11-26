package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
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

// GetInferenceModels returns the inference models
func (service *InferenceQueryService) GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelsResult, error) {
	inferenceModels, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, errors.New(apiError.FirestoreError)
	}

	var m = sync.Mutex{}
	eg, _ := errgroup.WithContext(ctx)

	inferenceModelsResult := make([]types.GetInferenceModelsResult, len(inferenceModels))

	// set limit
	eg.SetLimit(len(inferenceModels))

	for i, inferenceModel := range inferenceModels {
		func(inferenceModel entity.InferenceModel) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				containerInfo, err := service.GetContainerInfo(ctx, inferenceModel.ContainerID)
				if err != nil {
					return err
				}

				inferenceModelsResult[i] = types.GetInferenceModelsResult{
					ID:       inferenceModel.ID,
					TenantID: inferenceModel.TenantID,
					ContainerInfo: types.GetContainerInfoResult{
						ID:              containerInfo.ID,
						Name:            containerInfo.Name,
						Status:          containerInfo.Status,
						Running:         containerInfo.Running,
						StartedAt:       containerInfo.StartedAt,
						FinishedAt:      containerInfo.FinishedAt,
						CPUPercentUsage: containerInfo.CPUPercentUsage,
						MemoryInBytes:   containerInfo.MemoryInBytes,
					},
					Name:        inferenceModel.Name,
					DockerImage: inferenceModel.DockerImage,
					Envs:        inferenceModel.Envs,
					OutputMode:  inferenceModel.OutputMode,
					CreatedAt:   time.Unix(int64(inferenceModel.CreatedAt), 0),
					UpdatedAt:   time.Unix(int64(inferenceModel.UpdatedAt), 0),
				}

				return nil
			})
		}(inferenceModel)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return inferenceModelsResult, nil
}
