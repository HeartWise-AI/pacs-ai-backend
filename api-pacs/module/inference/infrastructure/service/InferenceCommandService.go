package service

import (
	"bytes"
	"context"
	"errors"
	"log"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/suyashkumar/dicom"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	dicomUtils "api-pacs/internal/dicoms"
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
	/// download dicom from query ids
	// TODO: remove this
	downloadStartTime := time.Now()

	var inferences [][][][][]int
	var age int
	var gender string

	for i, queryID := range data.QueryIDs {
		// download DICOM file
		dicomBytes, err := service.OrthancAPIInterface.DownloadDICOM(ctx, queryID)
		if err != nil {
			return dockerInferenceTypes.PredictResponse{}, err
		}

		// parse DICOM
		dataset, err := dicom.Parse(bytes.NewReader(dicomBytes), int64(len(dicomBytes)), nil)
		if err != nil {
			log.Println(err)
			return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DICOMParseError)
		}

		// get age and gender from first DICOM
		if i == 0 {
			// get age from first DICOM
			age, err = dicomUtils.ParseAge(dataset)
			if err != nil {
				log.Println(err)
				return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DICOMParseError)
			}

			// get gender from first DICOM
			gender, err = dicomUtils.ParseGender(dataset)
			if err != nil {
				log.Println(err)
				return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DICOMParseError)
			}
		}

		// convert DICOM to instances
		instance, err := dicomUtils.DICOMToInstances(dataset)
		if err != nil {
			log.Println(err)
			return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DICOMParseError)
		}

		inferences = append(inferences, instance)
	}

	// TODO: remove this
	downloadEndTime := time.Since(downloadStartTime)
	log.Printf("[prediction] download DICOM file took %f seconds", downloadEndTime.Seconds())

	/// send to docker inference model
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, errors.New(apiError.DockerError)
	}

	predictionResult, err := service.DockerInferenceAPIInterface.Predict(ctx, containerInfo.Name, dockerInferenceTypes.PredictRequest{
		Inferences: inferences,
		Age:        uint(age),
		Gender:     dockerInferenceTypes.Gender(gender),
		OutputMode: dockerInferenceTypes.OutputMode(data.OutputMode),
	})
	if err != nil {
		return dockerInferenceTypes.PredictResponse{}, err
	}

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
