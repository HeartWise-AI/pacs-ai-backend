package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQueryService handles the Inference query service logic
type InferenceQueryService struct {
	repository.InferenceQueryRepositoryInterface
	dockerTypes.DockerSDKInterface
	dockerInferenceTypes.DockerInferenceAPIInterface
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
		TenantID:           data.TenantID,
		IngestionJobID:     data.IngestionJobID,
		StudyInstanceUID:   data.StudyInstanceUID,
		Status:             status,
		RetrievalFailures:  data.RetrievalFailures,
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
	if tenantID == "" || data.Limit < 0 || data.Offset < 0 {
		return types.WorklistStudyStatusPage{}, errors.New(apiError.InvalidPayload)
	}

	limit := data.Limit
	if limit == 0 {
		limit = defaultWorklistPageLimit
	}
	if limit > maximumWorklistPageLimit {
		return types.WorklistStudyStatusPage{}, errors.New(apiError.MaximumLimitReached)
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
