package repository

import (
	"context"
	"time"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// ActiveModelRegistrationReader is optional for repositories supporting the
// private deployment registry. Existing query implementations remain compatible.
type ActiveModelRegistrationReader interface {
	ListActiveModelRegistrations(ctx context.Context) ([]entity.InferenceModel, error)
}

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
	// SelectInferenceIngestionCandidateByID gets one ingestion candidate by id
	SelectInferenceIngestionCandidateByID(ID string) (entity.InferenceIngestionCandidate, error)
	// SelectInferenceIngestionProcessingJobByCandidateModel gets the latest run-aware processing job by candidate/model.
	SelectInferenceIngestionProcessingJobByCandidateModel(candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error)
	// ListInferenceIngestionCandidates lists ingestion candidates for debugging and operations
	ListInferenceIngestionCandidates(data types.ListInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error)
	// ListWorklistStudyStatuses returns one tenant-scoped current status per logical study.
	ListWorklistStudyStatuses(data types.ListWorklistStudyStatuses) (types.WorklistStudyStatusPage, error)
	// ListCandidatesByJob lists ingestion candidates by ingestion job ID
	ListCandidatesByJob(ingestionJobID string) ([]entity.InferenceIngestionCandidate, error)
	// ListCandidatesReadyForRetrieval lists stable ingestion candidates ready for retrieval
	ListCandidatesReadyForRetrieval(ingestionJobID string, stableBefore time.Time) ([]entity.InferenceIngestionCandidate, error)
	// ListCandidatesQueuedForRetrieval lists ingestion candidates queued for retrieval
	ListCandidatesQueuedForRetrieval() ([]entity.InferenceIngestionCandidate, error)
	// ListStaleProcessingCandidates lists candidates whose processing state appears stale.
	ListStaleProcessingCandidates(staleBefore time.Time) ([]entity.InferenceIngestionCandidate, error)
	// SelectModelFeedbackByUserModelID get model feedback by user and model ID
	SelectModelFeedbackByUserModelID(ctx context.Context, data types.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error)
	// SelectModelFeedbackAnswersByFeedbackID get model feedback answers by feedback ID
	SelectModelFeedbackAnswersByFeedbackID(ctx context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error)
	// SelectModelFeedbacksByTenantID gets every model feedback of a tenant; an empty result is an empty slice
	SelectModelFeedbacksByTenantID(ctx context.Context, tenantID string) ([]entity.ModelFeedback, error)
	// SelectModelFeedbackAnswersByFeedbackIDs gets the answers of the given feedback ids; an empty result is an empty slice.
	// Answers carry no tenant field, so callers must only pass ids of tenant-scoped feedbacks.
	SelectModelFeedbackAnswersByFeedbackIDs(ctx context.Context, feedbackIDs []string) ([]entity.ModelFeedbackAnswer, error)
	// SelectOnboardingModelQuestionnaireAnswers select onboarding model questionnaire answers
	SelectOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error)
}
