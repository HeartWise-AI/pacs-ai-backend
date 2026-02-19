package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
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

// SelectInferenceModelByContainerID get inference model by container
func (repository *InferenceQueryRepositoryCircuitBreaker) SelectInferenceModelByContainer(ctx context.Context, tenantID, containerID string) (entity.InferenceModel, error) {
	output := make(chan entity.InferenceModel, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_inference_model_by_container", config.Settings())
	errors := hystrix.Go("select_inference_model_by_container", func() error {
		inferenceModel, err := repository.InferenceQueryRepositoryInterface.SelectInferenceModelByContainer(ctx, tenantID, containerID)
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

// SelectModelFeedbackByUserModelID get model feedback by model ID
func (repository InferenceQueryRepositoryCircuitBreaker) SelectModelFeedbackByUserModelID(ctx context.Context, data repositoryTypes.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error) {
	output := make(chan entity.ModelFeedback, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_model_feedback_by_user_model_id", config.Settings())
	errors := hystrix.Go("select_model_feedback_by_user_model_id", func() error {
		modelFeedback, err := repository.InferenceQueryRepositoryInterface.SelectModelFeedbackByUserModelID(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- modelFeedback
		return nil
	}, nil)

	select {
	case modelFeedback := <-output:
		return modelFeedback, nil
	case err := <-errChan:
		return entity.ModelFeedback{}, err
	case err := <-errors:
		return entity.ModelFeedback{}, err
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

// SelectOnboardingModelQuestionnaireAnswers select onboarding model questionnaire answers
func (repository *InferenceQueryRepositoryCircuitBreaker) SelectOnboardingModelQuestionnaireAnswers(ctx context.Context, data repositoryTypes.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error) {
	output := make(chan []entity.OnboardingModelQuestionnaireAnswer, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_onboarding_model_questionnaire_answers", config.Settings())
	errors := hystrix.Go("select_onboarding_model_questionnaire_answers", func() error {
		answers, err := repository.InferenceQueryRepositoryInterface.SelectOnboardingModelQuestionnaireAnswers(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- answers
		return nil
	}, nil)

	select {
	case answers := <-output:
		return answers, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}
