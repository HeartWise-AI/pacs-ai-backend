package application

import (
	"context"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryServiceInterface holds the implementable methods for the inference query service
type InferenceQueryServiceInterface interface {
	// GetInferenceQuota returns the authenticated user's current quota state.
	GetInferenceQuota(ctx context.Context, tenantID, userID string) (types.InferenceQuotaStatus, error)
	// GetContainerInfo returns the container info with stats
	GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error)
	// GetInferenceModels returns the inference models
	GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelResult, error)
	// GetInferenceModelInfo gets the inference model info
	GetInferenceModelInfo(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelInfoResponse, error)
	// GetInferenceModelFacts gets the inference model facts
	GetInferenceModelFacts(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelFactsResponse, error)
	// GetInferenceAvailableModels gets the inference available models
	GetInferenceAvailableModels(ctx context.Context, tenantID string) ([]types.GetInferenceAvailableModelResult, error)
	// GetInferenceIngestionJobs gets the inference ingestion jobs
	GetInferenceIngestionJobs(ctx context.Context, tenantID string) ([]entity.InferenceIngestionJob, error)
	// GetInferenceIngestionCandidates gets ingestion candidates for debugging and operations
	GetInferenceIngestionCandidates(ctx context.Context, data types.GetInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error)
	// GetWorklistStudyStatuses returns the current tenant-scoped worklist snapshot.
	GetWorklistStudyStatuses(ctx context.Context, data types.GetWorklistStudyStatuses) (types.WorklistStudyStatusPage, error)
	// GetStudyProcessingRunHistory returns paginated run and model history for one study.
	GetStudyProcessingRunHistory(ctx context.Context, data types.GetStudyProcessingRunHistory) (types.StudyProcessingRunHistoryPage, error)
	// GetProcessingRunDetail returns one tenant-scoped run and its frozen model plan.
	GetProcessingRunDetail(ctx context.Context, data types.GetProcessingRunDetail) (types.ProcessingRunDetail, error)
	// GetProcessingRunExecutionResult lazily returns one correlated completed model result.
	GetProcessingRunExecutionResult(ctx context.Context, data types.GetProcessingRunExecutionResult) (types.ProcessingRunExecutionResult, error)
	// GetModelFeedBackByUser gets the model feedback by user
	GetModelFeedBackByUser(ctx context.Context, data types.GetModelFeedbackByUser) (types.GetModelFeedbackResult, error)
	// GetOnboardingModelQuestionnaireAnswers gets the onboarding model questionnaire answers
	GetOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error)
}
