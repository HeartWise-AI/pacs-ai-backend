package service

import (
	"context"

	"github.com/segmentio/ksuid"

	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceCommandService handles the Inference command service logic
type InferenceCommandService struct {
	repository.InferenceCommandRepositoryInterface
}

// AddInferenceModel adds an inference model
func (service *InferenceCommandService) AddInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	// TODO: launch container and get container ID

	ID := generateID()

	err := service.InferenceCommandRepositoryInterface.InsertInferenceModel(ctx, repositoryTypes.AddInferenceModel{
		ID:          ID,
		TenantID:    data.TenantID,
		Name:        data.Name,
		DockerImage: data.DockerImage,
		Envs:        data.Envs,
		OutputMode:  data.OutputMode,
	})
	if err != nil {
		return err
	}

	return nil
}

// DeleteInferenceModel deletes an inference model
func (service *InferenceCommandService) DeleteInferenceModel(ctx context.Context, ID string) error {
	// TODO: make sure the container is stopped

	err := service.InferenceCommandRepositoryInterface.DeleteInferenceModel(ctx, ID)
	if err != nil {
		return err
	}

	return nil
}

// UpdateInferenceModel updates an inference model
func (service *InferenceCommandService) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceModel(ctx, repositoryTypes.UpdateInferenceModel{
		ID:          data.ID,
		Name:        data.Name,
		DockerImage: data.DockerImage,
		Envs:        data.Envs,
		OutputMode:  data.OutputMode,
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateInferenceModelContainerID updates the container ID of an inference model
func (service *InferenceCommandService) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	err := service.InferenceCommandRepositoryInterface.UpdateInferenceModelContainerID(ctx, ID, containerID)
	if err != nil {
		return err
	}

	return nil
}

func generateID() string {
	return ksuid.New().String()
}
