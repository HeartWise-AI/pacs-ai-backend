package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
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
	dockerInferenceTypes.DockerInferenceAPIInterface
}

// GetContainerInfo returns the container info
func (service *InferenceQueryService) GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error) {
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return types.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}

	return types.GetContainerInfoResult{
		ID:              containerInfo.ID,
		Name:            containerInfo.Name[1:], // remove "/" prefix
		Status:          containerInfo.Status,
		Running:         containerInfo.Running,
		StartedAt:       containerInfo.StartedAt,
		FinishedAt:      containerInfo.FinishedAt,
		CPUPercentUsage: containerInfo.CPUPercentUsage,
		MemoryInBytes:   containerInfo.MemoryInBytes,
	}, nil
}

// GetInferenceModels returns the inference models
func (service *InferenceQueryService) GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelResult, error) {
	inferenceModels, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, errors.New(apiError.FirestoreError)
	}

	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	var inferenceModelsResult []types.GetInferenceModelResult

	// set limit
	eg.SetLimit(len(inferenceModels))

	for _, inferenceModel := range inferenceModels {
		func(inferenceModel entity.InferenceModel) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				containerInfo, err := service.GetContainerInfo(egCtx, inferenceModel.ContainerID)
				if err != nil {
					log.Println(err)
					return nil // skip error
				}

				inferenceModelsResult = append(inferenceModelsResult, types.GetInferenceModelResult{
					ID:       inferenceModel.ID,
					TenantID: inferenceModel.TenantID,
					Container: types.GetContainerInfoResult{
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
				})

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

// GetInferenceModelInfo gets the inference model info
func (service *InferenceQueryService) GetInferenceModelInfo(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	// get container name
	containerInfo, err := service.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, err
	}

	modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerInfo.Name)
	if err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return modelInfo, nil
}

// GetInferenceModelFacts gets the inference model facts
func (service *InferenceQueryService) GetInferenceModelFacts(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelFactsResponse, error) {
	// get container name
	containerInfo, err := service.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.GetModelFactsResponse{}, err
	}

	modelFacts, err := service.DockerInferenceAPIInterface.GetModelFacts(ctx, containerInfo.Name)
	if err != nil {
		return dockerInferenceTypes.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return modelFacts, nil
}

// GetInferenceAvailableModels gets the inference available models
func (service *InferenceQueryService) GetInferenceAvailableModels(ctx context.Context, tenantID string) ([]types.GetInferenceAvailableModelResult, error) {
	// get inference models
	inferenceModels, err := service.GetInferenceModels(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// get model info for each inference model
	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	var inferenceAvailableModels []types.GetInferenceAvailableModelResult

	// set limit
	eg.SetLimit(len(inferenceModels))

	for _, inferenceModel := range inferenceModels {
		func(inferenceModel types.GetInferenceModelResult) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				// check if container id is set and running
				if len(inferenceModel.Container.ID) > 0 && inferenceModel.Container.Running {
					// get model info
					modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(egCtx, inferenceModel.Container.Name)
					if err != nil {
						log.Println(err)
						return nil // skip error
					}

					modelFacts, err := service.DockerInferenceAPIInterface.GetModelFacts(egCtx, inferenceModel.Container.Name)
					if err != nil {
						log.Println(err)
						return nil // skip error
					}

					inferenceAvailableModels = append(inferenceAvailableModels, types.GetInferenceAvailableModelResult{
						ContainerID:                 inferenceModel.Container.ID,
						ContainerName:               inferenceModel.Container.Name,
						ModelName:                   modelInfo.Data.ModelName,
						ModelFacts:                  types.ModelFacts(modelFacts.Data),
						Version:                     modelInfo.Data.Version,
						DicomTargetLevel:            modelInfo.Data.DicomTargetLevel,
						DicomUploadMin:              modelInfo.Data.DicomUploadMin,
						DicomUploadMax:              modelInfo.Data.DicomUploadMax,
						SupportedDicomModalities:    modelInfo.Data.SupportedDicomModalities,
						SupportedAdditionalMetadata: modelInfo.Data.SupportedAdditionalMetadata,
						OutputMode:                  inferenceModel.OutputMode,
					})
				}

				return nil
			})
		}(inferenceModel)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return inferenceAvailableModels, nil
}
