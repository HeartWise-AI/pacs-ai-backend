package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryService handles the Inference query service logic
type InferenceQueryService struct {
	repository.InferenceQueryRepositoryInterface
	repository.InferenceProcessingRunRepositoryInterface
	dockerTypes.DockerSDKInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
	application.InferenceQuotaManagerInterface
	application.ProcessingResultProviderInterface
}

// GetInferenceQuota returns the current tenant/user allowance and active work.
func (service *InferenceQueryService) GetInferenceQuota(ctx context.Context, tenantID, userID string) (types.InferenceQuotaStatus, error) {
	if service.InferenceQuotaManagerInterface == nil {
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	return service.InferenceQuotaManagerInterface.Status(ctx, tenantID, userID)
}

const (
	defaultWorklistPageLimit = 25
	maximumWorklistPageLimit = 100
)

// GetContainerInfo returns the container info with stats
func (service *InferenceQueryService) GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error) {
	// get container info
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return types.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}

	// get container stats
	containerStats, err := service.DockerSDKInterface.GetContainerStats(ctx, containerID)
	if err != nil {
		return types.GetContainerInfoResult{}, errors.New(apiError.DockerError)
	}

	return types.GetContainerInfoResult{
		ID:              containerInfo.ID,
		Name:            containerInfo.Name[1:], // remove "/" prefix
		Status:          containerInfo.Status,
		Running:         containerInfo.Running,
		StartedAt:       containerInfo.StartedAt,
		FinishedAt:      containerInfo.FinishedAt,
		CPUPercentUsage: containerStats.CPUPercentUsage,
		MemoryInBytes:   containerStats.MemoryInBytes,
	}, nil
}

// GetInferenceModels returns the inference models
func (service *InferenceQueryService) GetInferenceModels(ctx context.Context, tenantID string) ([]types.GetInferenceModelResult, error) {
	inferenceModels, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, errors.New(apiError.FirestoreError)
	}

	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	var inferenceModelsResult []types.GetInferenceModelResult

	// set limit
	eg.SetLimit(len(inferenceModels))

	for _, inferenceModel := range inferenceModels {
		func(inferenceModel entity.InferenceModel) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				containerInfo, err := service.GetContainerInfo(egCtx, inferenceModel.ContainerID)
				if err != nil {
					log.Println(err)
					return nil // skip error
				}

				inferenceModelsResult = append(inferenceModelsResult, types.GetInferenceModelResult{
					ID:       inferenceModel.ID,
					TenantID: inferenceModel.TenantID,
					Container: types.GetContainerInfoResult{
						ID:              containerInfo.ID,
						Name:            containerInfo.Name,
						Status:          containerInfo.Status,
						Running:         containerInfo.Running,
						StartedAt:       containerInfo.StartedAt,
						FinishedAt:      containerInfo.FinishedAt,
						CPUPercentUsage: containerInfo.CPUPercentUsage,
						MemoryInBytes:   containerInfo.MemoryInBytes,
					},
					Name:                inferenceModel.Name,
					DockerImage:         inferenceModel.DockerImage,
					Envs:                inferenceModel.Envs,
					DisallowedDICOMTags: inferenceModel.DisallowedDICOMTags,
					OutputMode:          inferenceModel.OutputMode,
					CreatedAt:           time.Unix(int64(inferenceModel.CreatedAt), 0),
					UpdatedAt:           time.Unix(int64(inferenceModel.UpdatedAt), 0),
				})

				return nil
			})
		}(inferenceModel)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return inferenceModelsResult, nil
}

// GetInferenceModelInfo gets the inference model info
func (service *InferenceQueryService) GetInferenceModelInfo(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	// get container name
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, err
	}

	modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(ctx, containerInfo.Name[1:]) // remove "/" prefix
	if err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return modelInfo, nil
}

// GetInferenceModelFacts gets the inference model facts
func (service *InferenceQueryService) GetInferenceModelFacts(ctx context.Context, containerID string) (dockerInferenceTypes.GetModelFactsResponse, error) {
	// get container name
	containerInfo, err := service.DockerSDKInterface.GetContainerInfo(ctx, containerID)
	if err != nil {
		return dockerInferenceTypes.GetModelFactsResponse{}, err
	}

	modelFacts, err := service.DockerInferenceAPIInterface.GetModelFacts(ctx, containerInfo.Name[1:]) // remove "/" prefix
	if err != nil {
		return dockerInferenceTypes.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return modelFacts, nil
}

// GetInferenceAvailableModels gets the inference available models
func (service *InferenceQueryService) GetInferenceAvailableModels(ctx context.Context, tenantID string) ([]types.GetInferenceAvailableModelResult, error) {
	// get inference models
	inferenceModels, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	// get model info for each inference model
	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	var inferenceAvailableModels []types.GetInferenceAvailableModelResult

	// set limit
	eg.SetLimit(len(inferenceModels))

	for _, inferenceModel := range inferenceModels {
		func(inferenceModel entity.InferenceModel) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				containerInfo, err := service.DockerSDKInterface.GetContainerInfo(egCtx, inferenceModel.ContainerID)
				if err != nil {
					log.Println(err)
					return nil // skip error
				}

				containerName := containerInfo.Name[1:] // remove "/" prefix

				// check if container id is set and running
				if len(inferenceModel.ContainerID) > 0 && containerInfo.Running {
					// get model info
					modelInfo, err := service.DockerInferenceAPIInterface.GetModelInfo(egCtx, containerName)
					if err != nil {
						log.Println(err)
						return nil // skip error
					}

					// get model facts
					modelFacts, err := service.DockerInferenceAPIInterface.GetModelFacts(egCtx, containerName)
					if err != nil {
						log.Println(err)
						return nil // skip error
					}

					inferenceAvailableModels = append(inferenceAvailableModels, types.GetInferenceAvailableModelResult{
						ContainerID:                   inferenceModel.ContainerID,
						ContainerName:                 containerName,
						ModelID:                       modelInfo.Data.ModelID,
						ModelName:                     modelInfo.Data.ModelName,
						ModelFacts:                    types.ModelFacts(modelFacts.Data),
						Version:                       modelInfo.Data.Version,
						DicomTargetLevel:              modelInfo.Data.DicomTargetLevel,
						DicomUploadMin:                modelInfo.Data.DicomUploadMin,
						DicomUploadMax:                modelInfo.Data.DicomUploadMax,
						SupportedDicomModalities:      modelInfo.Data.SupportedDicomModalities,
						SupportedDicomTags:            modelInfo.Data.SupportedDicomTags,
						SupportedAdditionalMetadata:   modelInfo.Data.SupportedAdditionalMetadata,
						ApproveFeedbackQuestionnaires: modelInfo.Data.ApproveFeedbackQuestionnaires,
						RejectFeedbackQuestionnaires:  modelInfo.Data.RejectFeedbackQuestionnaires,
						OnboardingModelQuestionnaires: modelInfo.Data.OnboardingModelQuestionnaires,
						OutputMode:                    inferenceModel.OutputMode,
					})
				}

				return nil
			})
		}(inferenceModel)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return inferenceAvailableModels, nil
}

// GetInferenceIngestionJobs gets the inference ingestion jobs
func (controller *InferenceQueryService) GetInferenceIngestionJobs(ctx context.Context, tenantID string) ([]entity.InferenceIngestionJob, error) {
	res, err := controller.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(&tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}

// GetInferenceIngestionCandidates gets ingestion candidates for debugging and operations
func (service *InferenceQueryService) GetInferenceIngestionCandidates(ctx context.Context, data types.GetInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error) {
	var status *entity.InferenceIngestionCandidateStatus
	if data.Status != nil {
		parsedStatus, ok := parseInferenceIngestionCandidateStatus(*data.Status)
		if !ok {
			return nil, errors.New(apiError.InvalidPayload)
		}

		status = &parsedStatus
	}

	res, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID:          data.TenantID,
		IngestionJobID:    data.IngestionJobID,
		StudyInstanceUID:  data.StudyInstanceUID,
		Status:            status,
		RetrievalFailures: data.RetrievalFailures,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}

func parseInferenceIngestionCandidateStatus(status string) (entity.InferenceIngestionCandidateStatus, bool) {
	normalizedStatus := strings.ToUpper(status)

	switch entity.InferenceIngestionCandidateStatus(normalizedStatus) {
	case entity.InferenceIngestionCandidateStatusDiscovered,
		entity.InferenceIngestionCandidateStatusGrowing,
		entity.InferenceIngestionCandidateStatusStable,
		entity.InferenceIngestionCandidateStatusRetrievalQueued,
		entity.InferenceIngestionCandidateStatusRetrieved,
		entity.InferenceIngestionCandidateStatusDisappeared,
		entity.InferenceIngestionCandidateStatusFailed:
		return entity.InferenceIngestionCandidateStatus(normalizedStatus), true
	default:
		return "", false
	}
}

// GetWorklistStudyStatuses returns the current worklist snapshot for one
// authenticated tenant. The repository remains responsible for enforcing the
// tenant predicate; this layer normalizes input and maps persistence types into
// the public frontend contract.
func (service *InferenceQueryService) GetWorklistStudyStatuses(_ context.Context, data types.GetWorklistStudyStatuses) (types.WorklistStudyStatusPage, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	limit, err := normalizeWorklistPagination(tenantID, data.Limit, data.Offset)
	if err != nil {
		return types.WorklistStudyStatusPage{}, err
	}

	studyInstanceUIDs, valid := normalizeStudyInstanceUIDs(data.StudyInstanceUIDs)
	if !valid {
		return types.WorklistStudyStatusPage{}, errors.New(apiError.InvalidPayload)
	}

	repositoryPage, err := service.InferenceQueryRepositoryInterface.ListWorklistStudyStatuses(repositoryTypes.ListWorklistStudyStatuses{
		TenantID:          tenantID,
		StudyInstanceUIDs: studyInstanceUIDs,
		Limit:             limit,
		Offset:            data.Offset,
	})
	if err != nil {
		return types.WorklistStudyStatusPage{}, err
	}

	studies := make([]types.WorklistStudyStatus, 0, len(repositoryPage.Studies))
	for _, status := range repositoryPage.Studies {
		attentionReasons := status.AttentionReasons
		if attentionReasons == nil {
			attentionReasons = entity.InferenceIngestionProcessingRunAttentionReasons{}
		}

		studies = append(studies, types.WorklistStudyStatus{
			StudyInstanceUID:  status.StudyInstanceUID,
			IngestionStatus:   status.IngestionStatus,
			RetrievalState:    status.RetrievalState,
			RetrievalError:    status.RetrievalError,
			RunID:             status.RunID,
			RunNumber:         status.RunNumber,
			Trigger:           status.RunTrigger,
			Phase:             status.Phase,
			Outcome:           status.Outcome,
			AttentionRequired: status.AttentionRequired,
			AttentionReasons:  attentionReasons,
			ProcessingRunCounts: types.ProcessingRunCounts{
				Expected:  status.ExpectedModels,
				Pending:   status.PendingModels,
				Queued:    status.QueuedModels,
				Running:   status.RunningModels,
				Completed: status.CompletedModels,
				Failed:    status.FailedModels,
				Skipped:   status.SkippedModels,
				Cancelled: status.CancelledModels,
				Active:    status.ActiveModels,
			},
			Version:     status.Version,
			StartedAt:   status.StartedAt,
			CompletedAt: status.CompletedAt,
			UpdatedAt:   status.UpdatedAt,
		})
	}

	return types.WorklistStudyStatusPage{
		Studies: studies,
		WorklistPage: types.WorklistPage{
			Limit:   limit,
			Offset:  data.Offset,
			HasMore: repositoryPage.HasMore,
		},
	}, nil
}

func normalizeStudyInstanceUIDs(values []string) ([]string, bool) {
	if len(values) == 0 {
		return nil, true
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		uid := strings.TrimSpace(value)
		if uid == "" {
			return nil, false
		}
		if _, exists := seen[uid]; exists {
			continue
		}

		seen[uid] = struct{}{}
		normalized = append(normalized, uid)
	}

	return normalized, true
}

// GetStudyProcessingRunHistory returns newest-first runs and their frozen model
// executions for one tenant-scoped study.
func (service *InferenceQueryService) GetStudyProcessingRunHistory(ctx context.Context, data types.GetStudyProcessingRunHistory) (types.StudyProcessingRunHistoryPage, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	limit, err := normalizeWorklistPagination(tenantID, data.Limit, data.Offset)
	if err != nil {
		return types.StudyProcessingRunHistoryPage{}, err
	}
	if studyInstanceUID == "" {
		return types.StudyProcessingRunHistoryPage{}, errors.New(apiError.InvalidPayload)
	}

	repositoryPage, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunHistoryPage(ctx, repositoryTypes.ListInferenceIngestionProcessingRuns{
		TenantID: tenantID, StudyInstanceUID: studyInstanceUID, Limit: limit, Offset: data.Offset,
	})
	if err != nil {
		return types.StudyProcessingRunHistoryPage{}, err
	}

	runIDs := make([]string, 0, len(repositoryPage.Runs))
	for _, run := range repositoryPage.Runs {
		runIDs = append(runIDs, run.ID)
	}

	executions, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutionsByRunIDs(ctx, repositoryTypes.ListInferenceIngestionProcessingRunExecutions{
		TenantID: tenantID, ProcessingRunIDs: runIDs,
	})
	if err != nil {
		return types.StudyProcessingRunHistoryPage{}, err
	}

	executionsByRunID := make(map[string][]entity.InferenceIngestionProcessingJob, len(repositoryPage.Runs))
	for _, execution := range executions {
		if execution.ProcessingRunID == nil {
			continue
		}
		executionsByRunID[*execution.ProcessingRunID] = append(executionsByRunID[*execution.ProcessingRunID], execution)
	}

	runs := make([]types.ProcessingRunDetail, 0, len(repositoryPage.Runs))
	for _, run := range repositoryPage.Runs {
		runs = append(runs, buildProcessingRunDetail(run, executionsByRunID[run.ID]))
	}

	return types.StudyProcessingRunHistoryPage{
		Runs: runs,
		WorklistPage: types.WorklistPage{
			Limit: limit, Offset: data.Offset, HasMore: repositoryPage.HasMore,
		},
	}, nil
}

// GetProcessingRunDetail returns one run only when its ID belongs to the
// authenticated tenant, then loads the run's frozen model plan.
func (service *InferenceQueryService) GetProcessingRunDetail(ctx context.Context, data types.GetProcessingRunDetail) (types.ProcessingRunDetail, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	runID := strings.TrimSpace(data.RunID)
	if tenantID == "" || runID == "" {
		return types.ProcessingRunDetail{}, errors.New(apiError.InvalidPayload)
	}

	run, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunDetail{}, err
	}

	executions, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutions(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunDetail{}, err
	}

	return buildProcessingRunDetail(run, executions), nil
}

// GetProcessingRunExecutionResult returns an opaque result only after local
// tenant/run/execution ownership and upstream job correlation are validated.
func (service *InferenceQueryService) GetProcessingRunExecutionResult(ctx context.Context, data types.GetProcessingRunExecutionResult) (types.ProcessingRunExecutionResult, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	runID := strings.TrimSpace(data.RunID)
	executionID := strings.TrimSpace(data.ExecutionID)
	if tenantID == "" || runID == "" || executionID == "" {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InvalidPayload)
	}

	run, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunExecutionResult{}, err
	}
	execution, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRunExecutionByID(ctx, tenantID, runID, executionID)
	if err != nil {
		return types.ProcessingRunExecutionResult{}, err
	}
	if execution.Status != entity.InferenceIngestionProcessingJobStatusCompleted {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceExecutionResultNotAvailable)
	}
	if execution.CompletedAt == nil || execution.StudyServiceJobID == nil || strings.TrimSpace(*execution.StudyServiceJobID) == "" {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceExecutionResultInvalid)
	}
	if service.ProcessingResultProviderInterface == nil {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}

	jobID := strings.TrimSpace(*execution.StudyServiceJobID)
	job, found, err := service.ProcessingResultProviderInterface.GetJobResultByID(ctx, tenantID, jobID)
	if err != nil {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	if !found || !executionResultJobMatches(job.StudyServiceJob, run, execution, tenantID, runID, executionID, jobID) || !validExecutionResultJSON(job.ResultJSON) {
		return types.ProcessingRunExecutionResult{}, errors.New(apiError.InferenceExecutionResultInvalid)
	}

	return types.ProcessingRunExecutionResult{
		RunID:            runID,
		ExecutionID:      executionID,
		StudyInstanceUID: run.StudyInstanceUID,
		ModelName:        execution.ModelName,
		ModelVersion:     execution.ModelVersion,
		Status:           execution.Status,
		CompletedAt:      *execution.CompletedAt,
		Result:           append(json.RawMessage(nil), job.ResultJSON...),
	}, nil
}

func executionResultJobMatches(job types.StudyServiceJob, run entity.InferenceIngestionProcessingRun, execution entity.InferenceIngestionProcessingJob, tenantID, runID, executionID, jobID string) bool {
	if strings.TrimSpace(job.JobID) != jobID ||
		strings.TrimSpace(job.StudyInstanceUID) != strings.TrimSpace(run.StudyInstanceUID) ||
		trimmedPointerValue(job.TenantID) != tenantID ||
		trimmedPointerValue(job.ProcessingRunID) != runID ||
		trimmedPointerValue(job.CandidateID) != strings.TrimSpace(execution.CandidateID) ||
		strings.TrimSpace(job.ModelName) != strings.TrimSpace(execution.ModelName) ||
		!strings.EqualFold(strings.TrimSpace(job.Status), string(entity.InferenceIngestionProcessingJobStatusCompleted)) {
		return false
	}

	processingExecutionID := trimmedPointerValue(job.ProcessingExecutionID)
	if run.RunTrigger == entity.InferenceIngestionProcessingRunTriggerAuto {
		// Automatic study-service jobs intentionally use their tenant/run/candidate/
		// model/job correlation and may omit the manual-only execution identity.
		// If a rolling-upgrade job supplies one, it must still match exactly.
		if processingExecutionID != "" && processingExecutionID != executionID {
			return false
		}
	} else if processingExecutionID != executionID {
		return false
	}

	localVersion := trimmedPointerValue(execution.ModelVersion)
	return localVersion == "" || trimmedPointerValue(job.ModelVersion) == localVersion
}

func validExecutionResultJSON(result json.RawMessage) bool {
	trimmed := bytes.TrimSpace(result)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && json.Valid(trimmed)
}

func normalizeWorklistPagination(tenantID string, limit, offset int) (int, error) {
	if tenantID == "" || limit < 0 || offset < 0 {
		return 0, errors.New(apiError.InvalidPayload)
	}
	if limit == 0 {
		limit = defaultWorklistPageLimit
	}
	if limit > maximumWorklistPageLimit {
		return 0, errors.New(apiError.MaximumLimitReached)
	}
	return limit, nil
}

func buildProcessingRunDetail(run entity.InferenceIngestionProcessingRun, executions []entity.InferenceIngestionProcessingJob) types.ProcessingRunDetail {
	attentionReasons := run.AttentionReasons
	if attentionReasons == nil {
		attentionReasons = entity.InferenceIngestionProcessingRunAttentionReasons{}
	}

	executionSummaries := make([]types.ProcessingRunExecutionSummary, 0, len(executions))
	for index := range executions {
		execution := &executions[index]
		executionSummaries = append(executionSummaries, types.ProcessingRunExecutionSummary{
			ExecutionID:  execution.ID,
			ModelName:    execution.ModelName,
			ModelVersion: execution.ModelVersion,
			Modality:     execution.Modality,
			Status:       execution.Status,
			ErrorMessage: execution.ErrorMessage,
			SkipReason:   execution.GetSkipReason(),
			StartedAt:    execution.StartedAt,
			CompletedAt:  execution.CompletedAt,
			UpdatedAt:    execution.UpdatedAt,
		})
	}

	aggregate := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
		Run: run, Executions: executions,
	})

	return types.ProcessingRunDetail{
		ProcessingRunSummary: types.ProcessingRunSummary{
			RunID:             run.ID,
			StudyInstanceUID:  run.StudyInstanceUID,
			RunNumber:         run.RunNumber,
			Trigger:           run.RunTrigger,
			Phase:             run.Phase,
			Outcome:           run.Outcome,
			AttentionRequired: run.AttentionRequired,
			AttentionReasons:  attentionReasons,
			ProcessingRunCounts: types.ProcessingRunCounts{
				Expected:  aggregate.Counts.Expected,
				Pending:   aggregate.Counts.Pending,
				Queued:    aggregate.Counts.Queued,
				Running:   aggregate.Counts.Running,
				Completed: aggregate.Counts.Completed,
				Failed:    aggregate.Counts.Failed,
				Skipped:   aggregate.Counts.Skipped,
				Cancelled: aggregate.Counts.Cancelled,
				Active:    aggregate.Counts.Active,
			},
			Version:     run.Version,
			StartedAt:   run.StartedAt,
			CompletedAt: run.CompletedAt,
			CreatedAt:   run.CreatedAt,
			UpdatedAt:   run.UpdatedAt,
		},
		Executions: executionSummaries,
	}
}

// GetModelFeedBackByUser gets the model feedback by user
func (service *InferenceQueryService) GetModelFeedBackByUser(ctx context.Context, data types.GetModelFeedbackByUser) (types.GetModelFeedbackResult, error) {
	modelFeedback, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackByUserModelID(ctx, repositoryTypes.GetModelFeedbackByUserModelID{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		ModelID:  data.ModelID,
	})
	if err != nil {
		return types.GetModelFeedbackResult{}, err
	}

	var modelFeedbackAnswersResult []types.ModelFeedbackAnswerResult

	// get model feedback answers
	modelFeedbackAnswers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackID(ctx, modelFeedback.ID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return types.GetModelFeedbackResult{}, err
	}

	for _, modelFeedbackAnswer := range modelFeedbackAnswers {
		modelFeedbackAnswersResult = append(modelFeedbackAnswersResult, types.ModelFeedbackAnswerResult{
			ID:                     modelFeedbackAnswer.ID,
			ModelFeedbackID:        modelFeedbackAnswer.ModelFeedbackID,
			QuestionnaireID:        modelFeedbackAnswer.QuestionnaireID,
			QuestionnaireQuestion:  modelFeedbackAnswer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: modelFeedbackAnswer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   modelFeedbackAnswer.QuestionnaireAnswers,
		})
	}

	return types.GetModelFeedbackResult{
		ID:                   modelFeedback.ID,
		TenantID:             modelFeedback.TenantID,
		UserID:               modelFeedback.UserID,
		InferenceModelID:     modelFeedback.InferenceModelID,
		ModelID:              modelFeedback.ModelID,
		FeedbackType:         modelFeedback.FeedbackType,
		ModelFeedbackAnswers: modelFeedbackAnswersResult,
	}, nil
}

// GetOnboardingModelQuestionnaireAnswers gets the onboarding model questionnaire answers
func (service *InferenceQueryService) GetOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error) {
	res, err := service.InferenceQueryRepositoryInterface.SelectOnboardingModelQuestionnaireAnswers(ctx, repositoryTypes.GetOnboardingModelQuestionnaireAnswer{
		TenantID: data.TenantID,
		UserID:   data.UserID,
		ModelID:  data.ModelID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}
