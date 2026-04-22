package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

// InferenceQueryController request controller for inference query
type InferenceQueryController struct {
	application.InferenceQueryServiceInterface
}

// GetContainerInfo returns the inference model container info with stats
func (controller *InferenceQueryController) GetContainerInfo(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	containerInfo, err := controller.InferenceQueryServiceInterface.GetContainerInfo(context.TODO(), containerID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference model container info.",
		Data: &types.GetContainerInfoResponse{
			ID:         containerInfo.ID,
			Name:       containerInfo.Name,
			Status:     containerInfo.Status,
			Running:    containerInfo.Running,
			StartedAt:  uint64(containerInfo.StartedAt.Unix()),
			FinishedAt: uint64(containerInfo.FinishedAt.Unix()),
		},
	}

	response.JSON(w)
}

// GetInferenceModels returns the inference models
func (controller *InferenceQueryController) GetInferenceModels(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	inferenceModels, err := controller.InferenceQueryServiceInterface.GetInferenceModels(context.TODO(), tenantID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	inferenceModelsResponse := []types.GetInferenceModelResponse{}
	for _, inferenceModel := range inferenceModels {
		// set env to empty if nil
		envs := inferenceModel.Envs
		if envs == nil {
			envs = []string{}
		}

		// set disallowedDICOMTags to empty if nil
		disallowedDICOMTags := inferenceModel.DisallowedDICOMTags
		if disallowedDICOMTags == nil {
			disallowedDICOMTags = []string{}
		}

		inferenceModelsResponse = append(inferenceModelsResponse, types.GetInferenceModelResponse{
			ID:       inferenceModel.ID,
			TenantID: inferenceModel.TenantID,
			Container: types.GetContainerInfoResponse{
				ID:              inferenceModel.Container.ID,
				Name:            inferenceModel.Container.Name,
				Status:          inferenceModel.Container.Status,
				Running:         inferenceModel.Container.Running,
				StartedAt:       uint64(inferenceModel.Container.StartedAt.Unix()),
				FinishedAt:      uint64(inferenceModel.Container.FinishedAt.Unix()),
				CPUPercentUsage: inferenceModel.Container.CPUPercentUsage,
				MemoryInBytes:   inferenceModel.Container.MemoryInBytes,
			},
			Name:                inferenceModel.Name,
			DockerImage:         inferenceModel.DockerImage,
			Envs:                envs,
			DisallowedDICOMTags: disallowedDICOMTags,
			OutputMode:          inferenceModel.OutputMode,
			CreatedAt:           uint64(inferenceModel.CreatedAt.Unix()),
			UpdatedAt:           uint64(inferenceModel.UpdatedAt.Unix()),
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference models.",
		Data:    inferenceModelsResponse,
	}

	response.JSON(w)
}

// GetInferenceModelInfo returns the inference model info
func (controller *InferenceQueryController) GetInferenceModelInfo(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	modelInfo, err := controller.InferenceQueryServiceInterface.GetInferenceModelInfo(context.TODO(), containerID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case apiError.DockerInferenceError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker inference service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference model info.",
		Data:    modelInfo.Data,
	}

	response.JSON(w)
}

// GetInferenceModelFacts returns the inference model facts
func (controller *InferenceQueryController) GetInferenceModelFacts(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	modelFacts, err := controller.InferenceQueryServiceInterface.GetInferenceModelFacts(context.TODO(), containerID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case apiError.DockerInferenceError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker inference service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference model facts.",
		Data:    modelFacts.Data,
	}

	response.JSON(w)
}

// GetInferenceAvailableModels returns the inference available models
func (controller *InferenceQueryController) GetInferenceAvailableModels(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	inferenceAvailableModels, err := controller.InferenceQueryServiceInterface.GetInferenceAvailableModels(context.TODO(), tenantID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "No records found."
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore service encountered an error."
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case apiError.DockerInferenceError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker inference service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	inferenceAvailableModelsResponse := []types.GetInferenceAvailableModelResponse{}
	for _, inferenceAvailableModel := range inferenceAvailableModels {
		inferenceAvailableModelsResponse = append(inferenceAvailableModelsResponse, types.GetInferenceAvailableModelResponse{
			ContainerID:                   inferenceAvailableModel.ContainerID,
			ContainerName:                 inferenceAvailableModel.ContainerName,
			ModelID:                       inferenceAvailableModel.ModelID,
			ModelName:                     inferenceAvailableModel.ModelName,
			ModelFacts:                    types.ModelFacts(inferenceAvailableModel.ModelFacts),
			Version:                       inferenceAvailableModel.Version,
			DicomTargetLevel:              inferenceAvailableModel.DicomTargetLevel,
			DicomUploadMin:                inferenceAvailableModel.DicomUploadMin,
			DicomUploadMax:                inferenceAvailableModel.DicomUploadMax,
			SupportedDicomModalities:      inferenceAvailableModel.SupportedDicomModalities,
			SupportedDicomTags:            inferenceAvailableModel.SupportedDicomTags,
			SupportedAdditionalMetadata:   inferenceAvailableModel.SupportedAdditionalMetadata,
			ApproveFeedbackQuestionnaires: inferenceAvailableModel.ApproveFeedbackQuestionnaires,
			RejectFeedbackQuestionnaires:  inferenceAvailableModel.RejectFeedbackQuestionnaires,
			OnboardingModelQuestionnaires: inferenceAvailableModel.OnboardingModelQuestionnaires,
			OutputMode:                    inferenceAvailableModel.OutputMode,
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference available models.",
		Data:    inferenceAvailableModelsResponse,
	}

	response.JSON(w)
}

// GetInferenceIngestionJobs get inference ingestion jobs
func (controller *InferenceQueryController) GetInferenceIngestionJobs(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	res, err := controller.InferenceQueryServiceInterface.GetInferenceIngestionJobs(context.TODO(), tenantID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Database error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	inferenceIngestionJobs := []types.GetInferenceIngestionJobResponse{}

	for _, job := range res {
		var lastExecutedAt uint64
		if job.LastExecutedAt != nil {
			lastExecutedAt = uint64(job.LastExecutedAt.Unix())
		}

		inferenceIngestionJobs = append(inferenceIngestionJobs, types.GetInferenceIngestionJobResponse{
			ID:                     job.ID,
			TenantID:               job.TenantID,
			DICOMModality:          job.DICOMModality,
			ContainerID:            job.ContainerID,
			ModelID:                job.ModelID,
			ModelName:              job.ModelName,
			ModelVersion:           job.ModelVersion,
			Modalities:             job.Modalities,
			StabilityMinutes:       job.StabilityMinutes,
			RecentWindowMinutes:    job.RecentWindowMinutes,
			MissingPollsThreshold:  job.MissingPollsThreshold,
			StudyTimeStart:         dereferenceString(job.StudyTimeStart),
			StudyTimeEnd:           dereferenceString(job.StudyTimeEnd),
			ScheduleStartTimestamp: scheduleTimestampUnix(job.ScheduleStartTimestamp),
			ScheduleEndTimestamp:   scheduleTimestampUnix(job.ScheduleEndTimestamp),
			Status:                 string(job.Status),
			LastExecutedAt:         lastExecutedAt,
			CreatedAt:              uint64(job.CreatedAt.Unix()),
			UpdatedAt:              uint64(job.UpdatedAt.Unix()),
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference ingestion jobs.",
		Data:    inferenceIngestionJobs,
	}

	response.JSON(w)
}

// GetInferenceIngestionCandidates gets ingestion candidates for debugging and operations
func (controller *InferenceQueryController) GetInferenceIngestionCandidates(w http.ResponseWriter, r *http.Request) {
	controller.getInferenceIngestionCandidates(w, r, nil, nil, false)
}

// GetInferenceIngestionJobCandidates gets ingestion candidates for one job
func (controller *InferenceQueryController) GetInferenceIngestionJobCandidates(w http.ResponseWriter, r *http.Request) {
	ingestionJobID := chi.URLParam(r, "ID")
	if len(ingestionJobID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid ingestion job ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	controller.getInferenceIngestionCandidates(w, r, &ingestionJobID, nil, false)
}

// GetInferenceIngestionCandidateByStudyUID gets candidate state for one study in a job
func (controller *InferenceQueryController) GetInferenceIngestionCandidateByStudyUID(w http.ResponseWriter, r *http.Request) {
	ingestionJobID := chi.URLParam(r, "ID")
	if len(ingestionJobID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid ingestion job ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	studyInstanceUID := chi.URLParam(r, "studyInstanceUID")
	if len(studyInstanceUID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid study instance UID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	controller.getInferenceIngestionCandidates(w, r, &ingestionJobID, &studyInstanceUID, false)
}

// GetInferenceIngestionRetrievalFailures gets candidates with failed retrieval state
func (controller *InferenceQueryController) GetInferenceIngestionRetrievalFailures(w http.ResponseWriter, r *http.Request) {
	controller.getInferenceIngestionCandidates(w, r, nil, nil, true)
}

func (controller *InferenceQueryController) getInferenceIngestionCandidates(w http.ResponseWriter, r *http.Request, ingestionJobID *string, studyInstanceUID *string, retrievalFailures bool) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	if ingestionJobID == nil {
		queryJobID := r.URL.Query().Get("jobId")
		if len(queryJobID) > 0 {
			ingestionJobID = &queryJobID
		}
	}

	if studyInstanceUID == nil {
		queryStudyInstanceUID := r.URL.Query().Get("studyInstanceUID")
		if len(queryStudyInstanceUID) > 0 {
			studyInstanceUID = &queryStudyInstanceUID
		}
	}

	var status *string
	queryStatus := r.URL.Query().Get("status")
	if len(queryStatus) > 0 {
		status = &queryStatus
	}

	res, err := controller.InferenceQueryServiceInterface.GetInferenceIngestionCandidates(context.TODO(), serviceTypes.GetInferenceIngestionCandidates{
		TenantID:           tenantID,
		IngestionJobID:     ingestionJobID,
		StudyInstanceUID:   studyInstanceUID,
		Status:             status,
		RetrievalFailures:  retrievalFailures,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.InvalidPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid ingestion candidate status."
		case apiError.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Database error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference ingestion candidates.",
		Data:    buildInferenceIngestionCandidateResponses(res),
	}

	response.JSON(w)
}

func buildInferenceIngestionCandidateResponses(candidates []entity.InferenceIngestionCandidate) []types.GetInferenceIngestionCandidateResponse {
	response := []types.GetInferenceIngestionCandidateResponse{}

	for _, candidate := range candidates {
		orthancJobIDs := []string{}
		if candidate.OrthancJobIDs != nil {
			orthancJobIDs = []string(candidate.OrthancJobIDs)
		}

		response = append(response, types.GetInferenceIngestionCandidateResponse{
			ID:                        candidate.ID,
			TenantID:                  candidate.TenantID,
			IngestionJobID:            candidate.IngestionJobID,
			StudyInstanceUID:          candidate.StudyInstanceUID,
			StudyDate:                 dereferenceString(candidate.StudyDate),
			StudyTime:                 dereferenceString(candidate.StudyTime),
			ModalitiesInStudy:         dereferenceString(candidate.ModalitiesInStudy),
			PatientID:                 dereferenceString(candidate.PatientID),
			AccessionNumber:           dereferenceString(candidate.AccessionNumber),
			SeriesCount:               dereferenceInt(candidate.SeriesCount),
			InstanceCount:             dereferenceInt(candidate.InstanceCount),
			FirstSeenAt:               uint64(candidate.FirstSeenAt.Unix()),
			LastSeenAt:                uint64(candidate.LastSeenAt.Unix()),
			LastChangedAt:             uint64(candidate.LastChangedAt.Unix()),
			MissingPolls:              candidate.MissingPolls,
			Status:                    string(candidate.Status),
			RetrievalQueuedAt:         nullableTimestampUnix(candidate.RetrievalQueuedAt),
			RetrievedAt:               nullableTimestampUnix(candidate.RetrievedAt),
			OrthancJobIDs:             orthancJobIDs,
			LastRetrievalState:        dereferenceString(candidate.LastRetrievalState),
			LastRetrievalError:        dereferenceString(candidate.LastRetrievalError),
			LastRetrievalErrorDetails: dereferenceString(candidate.LastRetrievalErrorDetails),
			LastRetrievalCheckedAt:    nullableTimestampUnix(candidate.LastRetrievalCheckedAt),
			CreatedAt:                 uint64(candidate.CreatedAt.Unix()),
			UpdatedAt:                 uint64(candidate.UpdatedAt.Unix()),
		})
	}

	return response
}

func dereferenceString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func dereferenceInt(value *int) int {
	if value == nil {
		return 0
	}

	return *value
}

func nullableTimestampUnix(value *time.Time) uint64 {
	if value == nil {
		return 0
	}

	return scheduleTimestampUnix(*value)
}

func scheduleTimestampUnix(value time.Time) uint64 {
	if value.Year() < 1971 {
		return 0
	}

	return uint64(value.Unix())
}

// GetModelFeedbackByModelID gets the model feedback by model ID
func (controller *InferenceQueryController) GetModelFeedbackByModelID(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	modelID := chi.URLParam(r, "modelID")
	if len(modelID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid model ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	res, err := controller.InferenceQueryServiceInterface.GetModelFeedBackByUser(context.TODO(), serviceTypes.GetModelFeedbackByUser{
		TenantID: tenantID,
		UserID:   userID,
		ModelID:  modelID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore service encountered an error."
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "Model feedback not found."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	var modelFeedbackAnswers []types.ModelFeedbackAnswerResult

	if res.ModelFeedbackAnswers != nil {
		var answers []types.ModelFeedbackAnswerResult

		for _, modelFeedbackAnswer := range res.ModelFeedbackAnswers {
			answers = append(answers, types.ModelFeedbackAnswerResult{
				ID:                     modelFeedbackAnswer.ID,
				ModelFeedbackID:        modelFeedbackAnswer.ModelFeedbackID,
				QuestionnaireID:        modelFeedbackAnswer.QuestionnaireID,
				QuestionnaireQuestion:  modelFeedbackAnswer.QuestionnaireQuestion,
				QuestionnaireAnswerIDs: modelFeedbackAnswer.QuestionnaireAnswerIDs,
				QuestionnaireAnswers:   modelFeedbackAnswer.QuestionnaireAnswers,
			})
		}

		modelFeedbackAnswers = answers
	}

	modelFeedbackAnswer := types.GetModelFeedbackResponse{
		ID:                   res.ID,
		TenantID:             res.TenantID,
		UserID:               res.UserID,
		InferenceModelID:     res.InferenceModelID,
		ModelID:              res.ModelID,
		FeedbackType:         res.FeedbackType,
		ModelFeedbackAnswers: modelFeedbackAnswers,
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved model feedback.",
		Data:    modelFeedbackAnswer,
	}

	response.JSON(w)
}

// GetOnboardingModelQuestionnaireAnswers gets the onboarding model questionnaire answers
func (controller *InferenceQueryController) GetOnboardingModelQuestionnaireAnswers(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	modelID := r.URL.Query().Get("modelId")
	if len(modelID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid model ID.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	res, err := controller.InferenceQueryServiceInterface.GetOnboardingModelQuestionnaireAnswers(context.TODO(), serviceTypes.GetOnboardingModelQuestionnaireAnswer{
		TenantID: tenantID,
		UserID:   userID,
		ModelID:  &modelID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	onboardingModelQuestionnaireAnswers := []types.GetOnboardingModelQuestionnaireAnswerResponse{}

	for _, answer := range res {
		onboardingModelQuestionnaireAnswers = append(onboardingModelQuestionnaireAnswers, types.GetOnboardingModelQuestionnaireAnswerResponse{
			ID:                     answer.ID,
			TenantID:               answer.TenantID,
			UserID:                 answer.UserID,
			ModelID:                answer.ModelID,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
			CreatedAt:              uint64(answer.CreatedAt),
			UpdatedAt:              uint64(answer.UpdatedAt),
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved onboarding model questionnaire answers.",
		Data:    onboardingModelQuestionnaireAnswers,
	}

	response.JSON(w)
}
