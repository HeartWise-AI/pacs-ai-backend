package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceCommandRepositoryCircuitBreaker circuit breaker for inference command repository
type InferenceCommandRepositoryCircuitBreaker struct {
	repository.InferenceCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// DeleteInferenceModel is the decorator for the inference command repository to delete inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteInferenceModel(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_inference_model", config.Settings())
	errors := hystrix.Go("delete_inference_model", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteInferenceModel(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// InsertInferenceModel is the decorator for the inference command repository to insert inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_inference_model", config.Settings())
	errors := hystrix.Go("insert_inference_model", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertInferenceModel(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceModel is the decorator for the inference command repository to update inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_model", config.Settings())
	errors := hystrix.Go("update_inference_model", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceModel(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// UpdateInferenceModelContainerID is the decorator for the inference command repository to update the container ID of an inference model
func (repository *InferenceCommandRepositoryCircuitBreaker) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_inference_model_container_id", config.Settings())
	errors := hystrix.Go("update_inference_model_container_id", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpdateInferenceModelContainerID(ctx, ID, containerID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}
