package application

import (
	"context"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceCommandServiceInterface holds the implementable methods for the user command service
type InferenceCommandServiceInterface interface {
	// AddInferenceModel adds an inference model
	AddInferenceModel(ctx context.Context, data types.AddInferenceModel) error
	// AddOnboardingQuestionnaireAnswer adds onboarding questionnaire answer
	AddOnboardingQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error
	// DeleteInferenceModel deletes an inference model
	DeleteInferenceModel(ctx context.Context, ID string) error
	// GenerateInferenceModelPredictRequest generates the prediction request payload
	GenerateInferenceModelPredictRequest(ctx context.Context, tenantID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictRequest, string, error)
	// PredictInferenceModel predicts an inference model
	PredictInferenceModel(ctx context.Context, tenantID, userID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error)
	// RemoveModelFeedback removes model feedback
	RemoveModelFeedback(ctx context.Context, data types.RemoveModelFeedback) error
	// RestartInferenceModelContainer restarts an inference model container
	RestartInferenceModelContainer(ctx context.Context, containerID string) error
	// StartInferenceModelContainer starts an inference model container
	StartInferenceModelContainer(ctx context.Context, containerID string) error
	// StopInferenceModelContainer stops an inference model container
	StopInferenceModelContainer(ctx context.Context, containerID string) error
	// UpdateInferenceModel updates an inference model
	UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error
	// UpdateModelFeedback updates model feedback
	UpdateModelFeedback(ctx context.Context, data types.UpdateModelFeedback) error
}
