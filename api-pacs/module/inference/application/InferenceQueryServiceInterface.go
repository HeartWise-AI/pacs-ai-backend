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
	// GetContainerInfo returns the container runtime information.
	GetContainerInfo(ctx context.Context, tenantID, containerID string) (types.GetContainerInfoResult, error)
	// GetInferenceModels returns the inference models
	GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelResult, error)
	// GetInferenceModelVersions lists catalog versions for one tenant-owned registration.
	GetInferenceModelVersions(ctx context.Context, data types.GetInferenceModelVersions) ([]types.GetInferenceModelVersionResult, error)
	// GetInferenceModelInfo gets the inference model info
	GetInferenceModelInfo(ctx context.Context, tenantID, containerID string) (dockerInferenceTypes.GetModelInfoResponse, error)
	// GetInferenceModelFacts gets the inference model facts
	GetInferenceModelFacts(ctx context.Context, tenantID, containerID string) (dockerInferenceTypes.GetModelFactsResponse, error)
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
	// GetStudyLatestModelResults returns the latest completed result per model for one study.
	GetStudyLatestModelResults(ctx context.Context, data types.GetStudyLatestModelResults) (types.StudyLatestModelResults, error)
	// GetProcessingRunExecutionReport re-renders the HTML report of one completed execution.
	GetProcessingRunExecutionReport(ctx context.Context, data types.GetProcessingRunExecutionReport) (types.ModelReport, error)
	// GetStudyJobReport re-renders the HTML report of one legacy study-service job.
	GetStudyJobReport(ctx context.Context, data types.GetStudyJobReport) (types.ModelReport, error)
	// GetStudyJobResult returns the stored result JSON of one completed study-service job.
	GetStudyJobResult(ctx context.Context, data types.GetStudyJobResult) (types.StudyJobResult, error)
	// Run labels (PACS-447): free-text labels on one run (study-service job) of a study.
	GetStudyRunLabels(ctx context.Context, data types.GetRunLabels) (types.RunLabels, error)
	AddStudyRunLabel(ctx context.Context, data types.ChangeRunLabel) (types.RunLabels, bool, error)
	RemoveStudyRunLabel(ctx context.Context, data types.ChangeRunLabel) (types.RunLabels, error)
	GetRunLabelCatalog(ctx context.Context, tenantID string) (types.RunLabelCatalog, error)
	// GetModelFeedBackByUser gets the model feedback by user
	GetModelFeedBackByUser(ctx context.Context, data types.GetModelFeedbackByUser) (types.GetModelFeedbackResult, error)
	// ListModelFeedback lists the model feedback of the session tenant for workspace administrators.
	ListModelFeedback(ctx context.Context, data types.ListModelFeedback) (types.ModelFeedbackList, error)
	// GetOnboardingModelQuestionnaireAnswers gets the onboarding model questionnaire answers
	GetOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error)
}
