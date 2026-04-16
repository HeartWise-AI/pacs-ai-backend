package repository

import (
	"context"
	"time"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

type InferenceQueryRepositoryInterface interface {
	// SelectInferenceModelByID get inference model by id
	SelectInferenceModelByID(ctx context.Context, ID string) (entity.InferenceModel, error)
	// SelectInferenceModelByContainer get inference model by container
	SelectInferenceModelByContainer(ctx context.Context, tenantID, containerID string) (entity.InferenceModel, error)
	// SelectInferenceModels get inference models by tenant id
	SelectInferenceModels(ctx context.Context, tenantID string) ([]entity.InferenceModel, error)
	// SelectInferenceIngestionJobs get inference ingestion jobs
	SelectInferenceIngestionJobs(tenantID *string) ([]entity.InferenceIngestionJob, error)
	// SelectInferenceIngestionJobByID get inference ingestion job by id
	SelectInferenceIngestionJobByID(ID string) (entity.InferenceIngestionJob, error)
	// ListCandidatesByJob lists ingestion candidates by ingestion job ID
	ListCandidatesByJob(ingestionJobID string) ([]entity.InferenceIngestionCandidate, error)
	// ListCandidatesReadyForRetrieval lists stable ingestion candidates ready for retrieval
	ListCandidatesReadyForRetrieval(ingestionJobID string, stableBefore time.Time) ([]entity.InferenceIngestionCandidate, error)
	// SelectModelFeedbackByUserModelID get model feedback by user and model ID
	SelectModelFeedbackByUserModelID(ctx context.Context, data types.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error)
	// SelectModelFeedbackAnswersByFeedbackID get model feedback answers by feedback ID
	SelectModelFeedbackAnswersByFeedbackID(ctx context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error)
	// SelectOnboardingModelQuestionnaireAnswers select onboarding model questionnaire answers
	SelectOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error)
}
