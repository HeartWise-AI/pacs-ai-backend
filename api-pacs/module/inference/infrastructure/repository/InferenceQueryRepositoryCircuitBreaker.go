package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
)

// InferenceQueryRepositoryCircuitBreaker is the circuit breaker for the inference query repository
type InferenceQueryRepositoryCircuitBreaker struct {
	repository.InferenceQueryRepositoryInterface
}

// SelectInferenceModelByID get inference model by id
func (repository *InferenceQueryRepositoryCircuitBreaker) SelectInferenceModelByID(ctx context.Context, ID string) (entity.InferenceModel, error) {
	output := make(chan entity.InferenceModel, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_inference_model_by_id", config.Settings())
	errors := hystrix.Go("select_inference_model_by_id", func() error {
		inferenceModel, err := repository.InferenceQueryRepositoryInterface.SelectInferenceModelByID(ctx, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- inferenceModel
		return nil
	}, nil)

	select {
	case inferenceModel := <-output:
		return inferenceModel, nil
	case err := <-errChan:
		return entity.InferenceModel{}, err
	case err := <-errors:
		return entity.InferenceModel{}, err
	}
}

// SelectInferenceModels get inference models by tenant id
func (repository *InferenceQueryRepositoryCircuitBreaker) SelectInferenceModels(ctx context.Context, tenantID string) ([]entity.InferenceModel, error) {
	output := make(chan []entity.InferenceModel, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_inference_models", config.Settings())
	errors := hystrix.Go("select_inference_models", func() error {
		inferenceModels, err := repository.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- inferenceModels
		return nil
	}, nil)

	select {
	case inferenceModels := <-output:
		return inferenceModels, nil
	case err := <-errChan:
		return []entity.InferenceModel{}, err
	case err := <-errors:
		return []entity.InferenceModel{}, err
	}
}

// SelectModelFeedbacksByUserID get model feedbacks by user ID
func (repository *InferenceQueryRepositoryCircuitBreaker) SelectModelFeedbacksByUserID(ctx context.Context, userID string) ([]entity.ModelFeedback, error) {
	output := make(chan []entity.ModelFeedback, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_model_feedbacks_by_user_id", config.Settings())
	errors := hystrix.Go("select_model_feedbacks_by_user_id", func() error {
		modelFeedbacks, err := repository.InferenceQueryRepositoryInterface.SelectModelFeedbacksByUserID(ctx, userID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- modelFeedbacks
		return nil
	}, nil)

	select {
	case modelFeedback := <-output:
		return modelFeedback, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SelectModelFeedbackAnswersByFeedbackID get model feedback answers by feedback ID
func (repository *InferenceQueryRepositoryCircuitBreaker) SelectModelFeedbackAnswersByFeedbackID(ctx context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error) {
	output := make(chan []entity.ModelFeedbackAnswer, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_model_feedback_answers_by_feedback_id", config.Settings())
	errors := hystrix.Go("select_model_feedback_answers_by_feedback_id", func() error {
		modelFeedbackAnswers, err := repository.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, feedbackID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- modelFeedbackAnswers
		return nil
	}, nil)

	select {
	case modelFeedbackAnswers := <-output:
		return modelFeedbackAnswers, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}
