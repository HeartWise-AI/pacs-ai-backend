package rest

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/gocarina/gocsv"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	inferenceService "api-pacs/module/inference/infrastructure/service"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

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

	stabilityMinutes := resolveStabilityMinutes(request.StabilityMinutes, request.IntervalInMinutes)
	if stabilityMinutes == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidPayload,
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
		StabilityMinutes:       stabilityMinutes,
		RecentWindowMinutes:    request.RecentWindowMinutes,
		MissingPollsThreshold:  request.MissingPollsThreshold,
		StudyTimeStart:         request.StudyTimeStart,
		StudyTimeEnd:           request.StudyTimeEnd,
		ScheduleStartTimestamp: request.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   request.ScheduleEndTimestamp,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.InvalidPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid payload request."
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

// ReprocessStudy creates a new manual processing run for one tenant-scoped study.
func (controller *InferenceCommandController) ReprocessStudy(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)
	studyInstanceUID := strings.TrimSpace(chi.URLParam(r, "studyInstanceUID"))
	if studyInstanceUID == "" {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid Study Instance UID.",
			ErrorCode: apiError.InvalidPayload,
		}
		response.JSON(w)
		return
	}

	result, err := controller.InferenceCommandServiceInterface.CreateManualStudyProcessingRun(r.Context(), serviceTypes.CreateStudyProcessingRun{
		TenantID:         tenantID,
		StudyInstanceUID: studyInstanceUID,
		UserID:           &userID,
	})
	if err != nil {
		if writeInferenceQuotaError(w, err) {
			return
		}
		httpCode := http.StatusInternalServerError
		errorMessage := "Please contact technical support."
		switch err.Error() {
		case apiError.InvalidPayload:
			httpCode = http.StatusBadRequest
			errorMessage = "Invalid Study Instance UID."
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMessage = "No processing candidates were found for this study."
		case apiError.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMessage = "This study already has an active processing run."
		case apiError.DatabaseError:
			errorMessage = "Database error."
		case apiError.InferenceQuotaUnavailable:
			httpCode = http.StatusServiceUnavailable
			errorMessage = "Inference quota enforcement is temporarily unavailable."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMessage,
			ErrorCode: err.Error(),
		}
		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully created manual study processing run.",
		Data: &types.CreateStudyProcessingRunResponse{
			RunID:          result.Run.ID,
			RunNumber:      result.Run.RunNumber,
			Trigger:        result.Run.RunTrigger,
			Phase:          result.Run.Phase,
			ExpectedModels: len(result.Executions),
		},
	}
	response.JSON(w)
}

// StudyServiceProcessingCallback ingests internal processing callbacks from study-service.
func (controller *InferenceCommandController) StudyServiceProcessingCallback(w http.ResponseWriter, r *http.Request) {
	callbackToken := strings.TrimSpace(os.Getenv("STUDY_SERVICE_CALLBACK_TOKEN"))
	if callbackToken == "" {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusServiceUnavailable,
			Success:   false,
			Message:   "Study-service callback auth is not configured.",
			ErrorCode: apiError.MissingConfiguration,
		}

		response.JSON(w)
		return
	}

	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization == "" {
		inferenceService.ObserveStudyServiceProcessingCallback("unknown", "unauthorized")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusUnauthorized,
			Success:   false,
			Message:   "Missing Authorization header.",
			ErrorCode: apiError.UnauthorizedAccess,
		}

		response.JSON(w)
		return
	}

	scheme, token, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(strings.TrimSpace(scheme), "Bearer") || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(callbackToken)) != 1 {
		inferenceService.ObserveStudyServiceProcessingCallback("unknown", "unauthorized")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusUnauthorized,
			Success:   false,
			Message:   "Invalid Authorization header.",
			ErrorCode: apiError.UnauthorizedAccess,
		}

		response.JSON(w)
		return
	}

	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID == "" {
		inferenceService.ObserveStudyServiceProcessingCallback("unknown", "invalid_request")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "X-Request-ID header is required.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	candidateID := chi.URLParam(r, "candidate_id")
	if strings.TrimSpace(candidateID) == "" {
		inferenceService.ObserveStudyServiceProcessingCallback("unknown", "invalid_request")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid candidate ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.StudyServiceProcessingCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		inferenceService.ObserveStudyServiceProcessingCallback("unknown", "invalid_request")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := types.Validate.Struct(request)
	if err != nil {
		validationErrors := err.(validator.ValidationErrors)
		if len(validationErrors) > 0 {
			inferenceService.ObserveStudyServiceProcessingCallback(request.Status, "invalid_payload")
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[validationErrors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		inferenceService.ObserveStudyServiceProcessingCallback(request.Status, "invalid_request")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	startedAt, err := parseRFC3339Pointer(request.StartedAt)
	if err != nil {
		inferenceService.ObserveStudyServiceProcessingCallback(request.Status, "invalid_payload")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid started_at timestamp.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	completedAt, err := parseRFC3339Pointer(request.CompletedAt)
	if err != nil {
		inferenceService.ObserveStudyServiceProcessingCallback(request.Status, "invalid_payload")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid completed_at timestamp.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	occurredAt, err := parseRFC3339Pointer(request.OccurredAt)
	if err != nil {
		inferenceService.ObserveStudyServiceProcessingCallback(request.Status, "invalid_payload")
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid occurred_at timestamp.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	var skipReason *entity.InferenceIngestionProcessingJobSkipReason
	if request.SkipReason != nil {
		normalized, reasonErr := entity.NewInferenceIngestionProcessingJobSkipReason(
			string(request.SkipReason.Code),
			request.SkipReason.Message,
		)
		if reasonErr != nil {
			inferenceService.ObserveStudyServiceProcessingCallback(request.Status, "invalid_payload")
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Invalid skip reason.",
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}
		skipReason = &normalized
	}

	processingRunID := ""
	if request.ProcessingRunID != nil {
		processingRunID = strings.TrimSpace(*request.ProcessingRunID)
	}
	processingExecutionID := ""
	if request.ProcessingExecutionID != nil {
		processingExecutionID = strings.TrimSpace(*request.ProcessingExecutionID)
	}

	result, err := controller.InferenceCommandServiceInterface.HandleStudyServiceProcessingCallback(context.TODO(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID:           candidateID,
		RequestID:             requestID,
		EventID:               strings.TrimSpace(request.EventID),
		Sequence:              request.Sequence,
		OccurredAt:            occurredAt,
		TenantID:              strings.TrimSpace(request.TenantID),
		IngestionJobID:        strings.TrimSpace(request.IngestionJobID),
		PayloadCandidateID:    strings.TrimSpace(request.CandidateID),
		RetrievalAttemptID:    strings.TrimSpace(request.RetrievalAttemptID),
		ProcessingRunID:       processingRunID,
		ProcessingExecutionID: processingExecutionID,
		StudyInstanceUID:      request.StudyInstanceUID,
		ModelName:             request.ModelName,
		ModelVersion:          request.ModelVersion,
		Modality:              request.Modality,
		Status:                request.Status,
		SkipReason:            skipReason,
		ErrorMessage:          request.ErrorMessage,
		StudyServiceJobID:     request.StudyServiceJobID,
		StartedAt:             startedAt,
		CompletedAt:           completedAt,
	})
	if err != nil {
		var httpCode int
		var errorMsg string
		outcome := "error"

		switch err.Error() {
		case apiError.InvalidPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid payload request."
			outcome = "invalid_payload"
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "Ingestion candidate not found."
			outcome = "not_found"
		case apiError.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Database error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		inferenceService.ObserveStudyServiceProcessingCallback(request.Status, outcome)
		if occurredAt != nil {
			inferenceService.ObserveStudyServiceProcessingCallbackLag(request.Status, outcome, *occurredAt)
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

	inferenceService.ObserveStudyServiceProcessingCallback(request.Status, result.Outcome)
	if occurredAt != nil {
		inferenceService.ObserveStudyServiceProcessingCallbackLag(request.Status, result.Outcome, *occurredAt)
	}
	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully processed study-service callback.",
		Data: map[string]string{
			"outcome": result.Outcome,
		},
	}

	response.JSON(w)
}

// ImportInferenceIngestionJobsCSVFile import an inference ingestion jobs CSV file
func (controller *InferenceCommandController) ImportInferenceIngestionJobsCSVFile(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	// Allow a small amount of multipart framing overhead while keeping the CSV
	// file itself bounded to mediaMaxFileSize.
	maxRequestBytes := mediaMaxFileSize + mediaMultipartOverheadAllowance
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	err := r.ParseMultipartForm(mediaMaxFileSize)
	if err != nil {
		if requestbody.IsTooLarge(err) {
			requestbody.ObserveRejection(r, maxRequestBytes, "inference_ingestion_csv")
			requestbody.WriteTooLarge(w)
			return
		}
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid multipart request.",
			ErrorCode: errors.InvalidRequestPayload,
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
	if fileHeader.Size > mediaMaxFileSize {
		requestbody.ObserveRejection(r, mediaMaxFileSize, "inference_ingestion_csv")
		requestbody.WriteTooLarge(w)
		return
	}

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

		startTimestamp, err := parseOptionalCSVTimestamp(csvJob.ScheduleStartTimestamp)
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

		endTimestamp, err := parseOptionalCSVTimestamp(csvJob.ScheduleEndTimestamp)
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
			StabilityMinutes:       csvJob.StabilityMinutes,
			RecentWindowMinutes:    csvJob.RecentWindowMinutes,
			MissingPollsThreshold:  csvJob.MissingPollsThreshold,
			StudyTimeStart:         csvJob.StudyTimeStart,
			StudyTimeEnd:           csvJob.StudyTimeEnd,
			ScheduleStartTimestamp: startTimestamp,
			ScheduleEndTimestamp:   endTimestamp,
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

	stabilityMinutes := resolveStabilityMinutes(request.StabilityMinutes, request.IntervalInMinutes)
	if stabilityMinutes == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.InferenceCommandServiceInterface.UpdateInferenceIngestionJob(context.TODO(), serviceTypes.UpdateInferenceIngestionJob{
		ID:                     ID,
		Modalities:             request.Modalities,
		StabilityMinutes:       stabilityMinutes,
		RecentWindowMinutes:    request.RecentWindowMinutes,
		MissingPollsThreshold:  request.MissingPollsThreshold,
		StudyTimeStart:         request.StudyTimeStart,
		StudyTimeEnd:           request.StudyTimeEnd,
		ScheduleStartTimestamp: request.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   request.ScheduleEndTimestamp,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.InvalidPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid payload request."
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

func parseOptionalCSVTimestamp(value string) (uint64, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return 0, nil
	}

	parsedTime, err := time.Parse("2006-01-02 15:04:05", trimmedValue)
	if err != nil {
		return 0, err
	}

	return uint64(parsedTime.Unix()), nil
}

func resolveStabilityMinutes(stabilityMinutes, intervalInMinutes uint) uint {
	if stabilityMinutes != 0 {
		return stabilityMinutes
	}

	return intervalInMinutes
}

func parseRFC3339Pointer(value *string) (*time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}

	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*value))
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}
