package rest

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/gocarina/gocsv"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

// InferenceCommandController request controller for inference command
type InferenceCommandController struct {
	application.InferenceCommandServiceInterface
}

var mediaMaxFileSize int64 = 5 * 1024 * 1024 // 5MB

var mediaAllowedFileTypes = []string{
	"text/csv", "application/csv",
}

// AddInferenceModel add a new inference model
func (controller *InferenceCommandController) AddInferenceModel(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.AddInferenceModelRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.InferenceCommandServiceInterface.AddInferenceModel(context.TODO(), serviceTypes.AddInferenceModel{
		TenantID:    tenantID,
		Name:        request.Name,
		DockerImage: request.DockerImage,
		Envs:        request.Envs,
		OutputMode:  request.OutputMode,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference model."
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
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully added inference model.",
	}

	response.JSON(w)
}

// CreateInferenceIngestionJob creates a new inference ingestion job
func (controller *InferenceCommandController) CreateInferenceIngestionJob(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.CreateInferenceIngestionJobRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.InferenceCommandServiceInterface.CreateInferenceIngestionJob(context.TODO(), serviceTypes.CreateInferenceIngestionJob{
		TenantID:               tenantID,
		DICOMModality:          request.DICOMModality,
		ContainerID:            request.ContainerID,
		ModelID:                request.ModelID,
		ModelName:              request.ModelName,
		ModelVersion:           request.ModelVersion,
		Modalities:             request.Modalities,
		IntervalInMinutes:      request.IntervalInMinutes,
		ScheduleStartTimestamp: request.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   request.ScheduleEndTimestamp,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference ingestion job."
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
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully created inference ingestion job.",
	}

	response.JSON(w)
}

// AddOnboardingModelQuestionnaireAnswers adds onboarding model questionnaire answers
func (controller *InferenceCommandController) AddOnboardingModelQuestionnaireAnswers(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.AddOnboardingModelQuestionnaireAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	onboardingModelQuestionnaires := []serviceTypes.OnboardingModelQuestionnaireAnswer{}
	for _, answer := range request.OnboardingModelQuestionnaireAnswers {
		onboardingModelQuestionnaire := serviceTypes.OnboardingModelQuestionnaireAnswer{
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		}

		onboardingModelQuestionnaires = append(onboardingModelQuestionnaires, onboardingModelQuestionnaire)
	}

	err = controller.InferenceCommandServiceInterface.AddOnboardingModelQuestionnaireAnswers(context.TODO(), serviceTypes.AddOnboardingModelQuestionnaireAnswer{
		TenantID:                            tenantID,
		UserID:                              userID,
		ModelID:                             request.ModelID,
		OnboardingModelQuestionnaireAnswers: onboardingModelQuestionnaires,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while adding onboarding model questionnaire answers."
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
		Message: "Successfully added onboarding model questionnaire answers.",
	}

	response.JSON(w)
}

// ImportInferenceIngestionJobsCSVFile import an inference ingestion jobs CSV file
func (controller *InferenceCommandController) ImportInferenceIngestionJobsCSVFile(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	// define max upload size
	r.Body = http.MaxBytesReader(w, r.Body, mediaMaxFileSize)
	err := r.ParseMultipartForm(mediaMaxFileSize)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Maximum file size reached.",
			ErrorCode: errors.MaximumLimitReached,
		}

		response.JSON(w)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		log.Println("Cannot read file:", err)
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Cannot read file.",
			ErrorCode: errors.InvalidPayload,
		}

		response.JSON(w)
		return
	}
	defer file.Close()

	mimeType := fileHeader.Header.Get("Content-Type")

	// limit allowed mime type only
	var isMimeTypeAllowed bool
	for _, allowedFileType := range mediaAllowedFileTypes {
		if mimeType == allowedFileType {
			isMimeTypeAllowed = true
		}
	}

	if !isMimeTypeAllowed {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid file type.",
			ErrorCode: errors.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	// sanitize filename
	filename := fileHeader.Filename
	if len(filename) > 200 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid file name.",
			ErrorCode: errors.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	var csvJobs []types.ImportInferenceIngestionJob
	if err := gocsv.Unmarshal(file, &csvJobs); err != nil {
		log.Println("Error parsing CSV:", err)
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid CSV format.",
			ErrorCode: errors.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	var jobs []serviceTypes.CreateInferenceIngestionJob
	for _, csvJob := range csvJobs {
		// handle modalities, split by comma and trim space
		modalitiesStr := strings.Trim(csvJob.Modalities, `"`)
		var modalities []string
		if len(modalitiesStr) != 0 {
			modalities = strings.Split(modalitiesStr, ",")
			for i, modality := range modalities {
				modalities[i] = strings.TrimSpace(modality)
			}
		}

		startTime, err := time.Parse("2006-01-02 15:04:05", csvJob.ScheduleStartTimestamp)
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Invalid timestamp format.",
				ErrorCode: errors.InvalidPayload,
			}
			response.JSON(w)
			return
		}

		endTime, err := time.Parse("2006-01-02 15:04:05", csvJob.ScheduleEndTimestamp)
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Invalid timestamp format.",
				ErrorCode: errors.InvalidPayload,
			}
			response.JSON(w)
			return
		}

		jobs = append(jobs, serviceTypes.CreateInferenceIngestionJob{
			TenantID:               tenantID,
			DICOMModality:          csvJob.DICOMModality,
			ContainerID:            csvJob.ContainerID,
			ModelID:                csvJob.ModelID,
			ModelName:              csvJob.ModelName,
			ModelVersion:           csvJob.ModelVersion,
			Modalities:             modalities,
			IntervalInMinutes:      csvJob.IntervalInMinutes,
			ScheduleStartTimestamp: uint64(startTime.Unix()),
			ScheduleEndTimestamp:   uint64(endTime.Unix()),
		})
	}

	err = controller.InferenceCommandServiceInterface.ImportInferenceIngestionJobs(context.TODO(), jobs)
	if err != nil {
		var httpCode int
		var errorMsg string
		errorCode := err.Error()

		switch errorCode {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "An error occurred while importing inference ingestion jobs."
		default:
			httpCode = http.StatusBadRequest
			errorMsg = "Please contact technical support."
			errorCode = errors.ServerError
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: errorCode,
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully imported inference ingestion jobs CSV file.",
	}

	response.JSON(w)
}

// PredictInferenceModel predicts an inference model
func (controller *InferenceCommandController) PredictInferenceModel(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

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

	var request types.PredictInferenceModelRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// check if empty
	if len(request.SeriesInstanceUIDs) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Series instance UIDs is empty.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// sort series instance by last part asc
	sort.Slice(request.SeriesInstanceUIDs, func(i, j int) bool {
		partsI := strings.Split(request.SeriesInstanceUIDs[i], ".")
		lastPartI, err := strconv.ParseInt(partsI[len(partsI)-1], 10, 64)
		if err != nil {
			return false
		}

		partsJ := strings.Split(request.SeriesInstanceUIDs[j], ".")
		lastPartJ, err := strconv.ParseInt(partsJ[len(partsJ)-1], 10, 64)
		if err != nil {
			return false
		}

		return lastPartI < lastPartJ
	})

	predictionResult, err := controller.InferenceCommandServiceInterface.PredictInferenceModel(context.TODO(), tenantID, containerID, &userID, serviceTypes.PredictInferenceModel{
		StudyInstanceUID:   request.StudyInstanceUID,
		SeriesInstanceUIDs: request.SeriesInstanceUIDs,
		AdditionalMetadata: request.AdditionalMetadata,
		ForceJSON:          request.ForceJSON,
	})
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
		Message: "Successfully applied prediction to inference model.",
		Data:    predictionResult.Data,
	}

	response.JSON(w)
}

// RemoveInferenceModel deletes an inference model
func (controller *InferenceCommandController) RemoveInferenceModel(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid inference model ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.RemoveInferenceModel(context.TODO(), ID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference model."
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
		Message: "Successfully deleted inference model.",
	}

	response.JSON(w)
}

// RemoveInferenceIngestionJob deletes an inference ingestion job
func (controller *InferenceCommandController) RemoveInferenceIngestionJob(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid inference model ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.RemoveInferenceIngestionJob(context.TODO(), ID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference ingestion job."
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
		Message: "Successfully deleted inference ingestion job.",
	}

	response.JSON(w)
}

// RemoveModelFeedback removes model feedback
func (controller *InferenceCommandController) RemoveModelFeedback(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	modelID := chi.URLParam(r, "modelID")
	if len(modelID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Model id is required.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.RemoveModelFeedback(context.TODO(), serviceTypes.RemoveModelFeedback{
		TenantID: tenantID,
		UserID:   userID,
		ModelID:  modelID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while removing model feedback."
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
		Message: "Successfully removed model feedback.",
	}

	response.JSON(w)
}

// RestartInferenceModelContainer restarts an inference model container
func (controller *InferenceCommandController) RestartInferenceModelContainer(w http.ResponseWriter, r *http.Request) {
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

	err := controller.InferenceCommandServiceInterface.RestartInferenceModelContainer(context.TODO(), containerID)
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
		Message: "Successfully restarted inference model container.",
	}

	response.JSON(w)
}

// StartInferenceModelContainer starts an inference model container
func (controller *InferenceCommandController) StartInferenceModelContainer(w http.ResponseWriter, r *http.Request) {
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

	err := controller.InferenceCommandServiceInterface.StartInferenceModelContainer(context.TODO(), containerID)
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
		Message: "Successfully started inference model container.",
	}

	response.JSON(w)
}

// StartInferenceIngestionJob starts an inference ingestion job
func (controller *InferenceCommandController) StartInferenceIngestionJob(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid job ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.StartInferenceIngestionJob(context.TODO(), ID)
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
		Message: "Successfully started inference ingestion job.",
	}

	response.JSON(w)
}

// StopInferenceModelContainer stops an inference model container
func (controller *InferenceCommandController) StopInferenceModelContainer(w http.ResponseWriter, r *http.Request) {
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

	err := controller.InferenceCommandServiceInterface.StopInferenceModelContainer(context.TODO(), containerID)
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
		Message: "Successfully stopped inference model container.",
	}

	response.JSON(w)
}

// StopInferenceInferenceJob stops an inference ingestion job
func (controller *InferenceCommandController) StopInferenceInferenceJob(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid job ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.StopInferenceIngestionJob(context.TODO(), ID)
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
		Message: "Successfully stopped inference ingestion job.",
	}

	response.JSON(w)
}

// UpdateInferenceModel update an inference model
func (controller *InferenceCommandController) UpdateInferenceModel(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid inference model ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.UpdateInferenceModelRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.InferenceCommandServiceInterface.UpdateInferenceModel(context.TODO(), serviceTypes.UpdateInferenceModel{
		ID:                  ID,
		DisallowedDICOMTags: request.DisallowedDICOMTags,
		OutputMode:          request.OutputMode,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference model."
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
		Message: "Successfully updated inference model.",
	}

	response.JSON(w)
}

// UpdateInferenceIngestionJob update an inference ingestion job
func (controller *InferenceCommandController) UpdateInferenceIngestionJob(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid job ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.UpdateInferenceIngestionJobRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.InferenceCommandServiceInterface.UpdateInferenceIngestionJob(context.TODO(), serviceTypes.UpdateInferenceIngestionJob{
		ID:                     ID,
		Modalities:             request.Modalities,
		IntervalInMinutes:      request.IntervalInMinutes,
		ScheduleStartTimestamp: request.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   request.ScheduleEndTimestamp,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference ingestion job."
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
		Message: "Successfully updated inference ingestion job.",
	}

	response.JSON(w)
}

// UpdateModelFeedback updates model feedback
func (controller *InferenceCommandController) UpdateModelFeedback(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.UpdateModelFeedbackRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// check if there are model feedbacks
	var modelFeedbackAnswers []serviceTypes.ModelFeedbackAnswer

	if len(request.ModelFeedbackAnswers) > 0 {
		for _, answer := range request.ModelFeedbackAnswers {
			modelFeedbackAnswers = append(modelFeedbackAnswers, serviceTypes.ModelFeedbackAnswer{
				ModelFeedbackID:        request.ID,
				QuestionnaireID:        answer.QuestionnaireID,
				QuestionnaireQuestion:  answer.QuestionnaireQuestion,
				QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
				QuestionnaireAnswers:   answer.QuestionnaireAnswers,
			})
		}
	}

	err = controller.InferenceCommandServiceInterface.UpdateModelFeedback(context.TODO(), serviceTypes.UpdateModelFeedback{
		ID:                   request.ID,
		TenantID:             tenantID,
		UserID:               userID,
		InferenceModelID:     request.InferenceModelID,
		ModelID:              request.ModelID,
		FeedbackType:         request.FeedbackType,
		ModelFeedbackAnswers: modelFeedbackAnswers,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating model feedback."
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
		Message: "Successfully updated model feedback.",
	}

	response.JSON(w)
}
