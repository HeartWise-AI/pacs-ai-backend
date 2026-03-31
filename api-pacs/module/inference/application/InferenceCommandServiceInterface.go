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
	// AddOnboardingModelQuestionnaireAnswers adds onboarding model questionnaire answers
	AddOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.AddOnboardingModelQuestionnaireAnswer) error
	// CreateInferenceIngestionJob creates an inference ingestion job
	CreateInferenceIngestionJob(ctx context.Context, data types.CreateInferenceIngestionJob) error
	// GenerateInferenceModelPredictRequest generates the prediction request payload
	GenerateInferenceModelPredictRequest(ctx context.Context, tenantID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictRequest, string, error)
	// PredictInferenceModel predicts an inference model
	PredictInferenceModel(ctx context.Context, tenantID, userID, containerID string, data types.PredictInferenceModel) (dockerInferenceTypes.PredictResponse, error)
	// RemoveInferenceModel deletes an inference model
	RemoveInferenceModel(ctx context.Context, ID string) error
	// RemoveInferenceIngestionJob removes an inference ingestion job
	RemoveInferenceIngestionJob(ctx context.Context, ID string) error
	// RemoveModelFeedback removes model feedback
	RemoveModelFeedback(ctx context.Context, data types.RemoveModelFeedback) error
	// RemoveOnboardingModelQuestionnaireAnswer removes an onboarding model questionnaire answer
	RemoveOnboardingModelQuestionnaireAnswer(ctx context.Context, ID string) error
	// RestartInferenceModelContainer restarts an inference model container
	RestartInferenceModelContainer(ctx context.Context, containerID string) error
	// StartInferenceModelContainer starts an inference model container
	StartInferenceModelContainer(ctx context.Context, containerID string) error
	// StartInferenceIngestionJob starts an inference ingestion job
	StartInferenceIngestionJob(ctx context.Context, jobID string) error
	// StopInferenceModelContainer stops an inference model container
	StopInferenceModelContainer(ctx context.Context, containerID string) error
	// StopInferenceIngestionJob stops an inference ingestion job
	StopInferenceIngestionJob(ctx context.Context, jobID string) error
	// UpdateInferenceModel updates an inference model
	UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error
	// UpdateInferenceIngestionJob updates an inference ingestion job
	UpdateInferenceIngestionJob(ctx context.Context, data types.UpdateInferenceIngestionJob) error
	// UpdateModelFeedback updates model feedback
	UpdateModelFeedback(ctx context.Context, data types.UpdateModelFeedback) error
}
