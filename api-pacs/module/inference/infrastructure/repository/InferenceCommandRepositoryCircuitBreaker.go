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

// DeleteModelFeedback is the decorator for the inference command repository to delete model feedback
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteModelFeedback(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_model_feedback", config.Settings())
	errors := hystrix.Go("delete_model_feedback", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteModelFeedback(ctx, ID)
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

// DeleteModelFeedbackAnswer is the decorator for the inference command repository to delete model feedback answer
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteModelFeedbackAnswer(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_model_feedback_answer", config.Settings())
	errors := hystrix.Go("delete_model_feedback_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteModelFeedbackAnswer(ctx, ID)
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

// DeleteOnboardingModelQuestionnaireAnswer is the decorator for the inference command repository to delete onboarding model questionnaire answer
func (repository *InferenceCommandRepositoryCircuitBreaker) DeleteOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_onboarding_model_questionnaire_answer", config.Settings())
	errors := hystrix.Go("delete_onboarding_model_questionnaire_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.DeleteOnboardingModelQuestionnaireAnswer(ctx, ID)
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

// InsertModelFeedbackAnswer is the decorator for the inference command repository to insert model feedback answer
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertModelFeedbackAnswer(ctx context.Context, data types.AddModelFeedbackAnswer) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_model_feedback_answer", config.Settings())
	errors := hystrix.Go("insert_model_feedback_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertModelFeedbackAnswer(ctx, data)
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

// InsertOnboardingModelQuestionnaireAnswer is the decorator for the inference command repository to insert onboarding model questionnaire answer
func (repository *InferenceCommandRepositoryCircuitBreaker) InsertOnboardingModelQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_onboarding_model_questionnaire_answer", config.Settings())
	errors := hystrix.Go("insert_onboarding_model_questionnaire_answer", func() error {
		err := repository.InferenceCommandRepositoryInterface.InsertOnboardingModelQuestionnaireAnswer(ctx, data)
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

// UpsertModelFeedback is the decorator for the inference command repository to upsert model feedback
func (repository *InferenceCommandRepositoryCircuitBreaker) UpsertModelFeedback(ctx context.Context, data types.UpsertModelFeedback) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("upsert_model_feedback", config.Settings())
	errors := hystrix.Go("upsert_model_feedback", func() error {
		err := repository.InferenceCommandRepositoryInterface.UpsertModelFeedback(ctx, data)
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
