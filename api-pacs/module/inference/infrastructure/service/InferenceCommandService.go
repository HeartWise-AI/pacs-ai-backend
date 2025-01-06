package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/segmentio/ksuid"
	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceCommandService handles the Inference command service logic
type InferenceCommandService struct {
	repository.InferenceCommandRepositoryInterface
	repository.InferenceQueryRepositoryInterface
	dockerTypes.DockerSDKInterface
	orthancAPITypes.OrthancAPIInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
}

// AddInferenceModel adds an inference model
func (service *InferenceCommandService) AddInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	// pull docker image
	err := service.DockerSDKInterface.PullImage(ctx, data.DockerImage)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// create container
	containerID, err := service.DockerSDKInterface.CreateContainer(ctx, dockerTypes.CreateContainer{
		Name:  data.Name,
		Image: data.DockerImage,
		Envs:  data.Envs,
	})
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// start container
	err = service.StartInferenceModelContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// generate ID
	ID := generateID()

	err = service.InferenceCommandRepositoryInterface.InsertInferenceModel(ctx, repositoryTypes.AddInferenceModel{
		ID:          ID,
		TenantID:    data.TenantID,
		ContainerID: containerID,
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
	// get inference model
	inferenceModel, err := service.InferenceQueryRepositoryInterface.SelectInferenceModelByID(ctx, ID)
	if err != nil {
		return err
	}

	// force remove container
	err = service.DockerSDKInterface.RemoveContainer(ctx, inferenceModel.ContainerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	// delete inference model
	err = service.InferenceCommandRepositoryInterface.DeleteInferenceModel(ctx, ID)
	if err != nil {
		return err
	}

	return nil
}

// PredictInferenceModel predicts an inference model
func (service *InferenceCommandService) PredictInferenceModel(ctx context.Context, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error) {
	// get series instances metadata
	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	seriesInstanceMetadata := map[int]map[int]interface{}{}

	// set limit
	eg.SetLimit(len(data.SeriesInstanceUIDs))

	for _, seriesInstanceUID := range data.SeriesInstanceUIDs {
		func(seriesInstanceUID string) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				// get instances
				instances, err := service.OrthancAPIInterface.GetDICOMWebSeriesInstances(egCtx, data.StudyInstanceUID, seriesInstanceUID)
				if err != nil {
					return err
				}

				for _, instance := range instances {
					seriesNumber := int(instance["00200011"].(map[string]interface{})["Value"].([]interface{})[0].(float64))
					sopInstanceUID := instance["00080018"].(map[string]interface{})["Value"].([]interface{})[0].(string)
					sopInstanceNumber := int(instance["00200013"].(map[string]interface{})["Value"].([]interface{})[0].(float64))

					instanceMetadata, err := service.OrthancAPIInterface.RetrieveDICOMWebInstanceMetadata(egCtx, data.StudyInstanceUID, seriesInstanceUID, sopInstanceUID)
					if err != nil {
						return err
					}

					seriesInstanceMetadata[seriesNumber][sopInstanceNumber] = instanceMetadata
				}

				return nil
			})
		}(seriesInstanceUID)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	fmt.Println(seriesInstanceMetadata)

	// TODO: remove this
	containerInfoStartTime := time.Now()

	/// send to docker inference model
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DockerError)
	}

	// TODO: remove this
	containerInfoEndTime := time.Since(containerInfoStartTime)
	log.Printf("[prediction] get container info took %f seconds", containerInfoEndTime.Seconds())

	// TODO: remove this
	predictionStartTime := time.Now()

	predictionResult, err := service.DockerInferenceAPIInterface.Predict(ctx, containerInfo.Name[1:], dockerInferenceTypes.PredictRequest{ // remove "/" prefix in container name
		SeriesInstanceMetadata: seriesInstanceMetadata,
		AdditionalMetadata:     data.AdditionalMetadata,
		OutputMode:             dockerInferenceTypes.OutputMode(data.OutputMode),
	})
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

	// TODO: remove this
	predictionEndTime := time.Since(predictionStartTime)
	log.Printf("[prediction] predict call took %f seconds", predictionEndTime.Seconds())

	return predictionResult, nil
}

// RestartInferenceModelContainer restarts an inference model container
func (service *InferenceCommandService) RestartInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.RestartContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StartInferenceModelContainer starts an inference model container
func (service *InferenceCommandService) StartInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.StartContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// StopInferenceModelContainer stops an inference model container
func (service *InferenceCommandService) StopInferenceModelContainer(ctx context.Context, containerID string) error {
	err := service.DockerSDKInterface.StopContainer(ctx, containerID)
	if err != nil {
		return errors.New(apiError.DockerError)
	}

	return nil
}

// TODO: check if needed
// UpdateInferenceModel updates an inference model
func (service *InferenceCommandService) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	// TODO: checks to only update
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

// TODO: check if needed
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
