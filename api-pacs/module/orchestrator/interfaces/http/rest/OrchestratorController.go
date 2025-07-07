package rest

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orchestrator/application"
	"api-pacs/module/orchestrator/infrastructure/service/types"
	httpTypes "api-pacs/module/orchestrator/interfaces/http"
)

// OrchestratorController handles orchestrator HTTP requests
type OrchestratorController struct {
	application.OrchestratorServiceInterface
}

// NewOrchestratorController creates a new orchestrator controller
func NewOrchestratorController(orchestratorService application.OrchestratorServiceInterface) *OrchestratorController {
	return &OrchestratorController{
		OrchestratorServiceInterface: orchestratorService,
	}
}

// CreateThread creates a new thread
func (controller *OrchestratorController) CreateThread(w http.ResponseWriter, r *http.Request) {
	var request types.CreateThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		fmt.Println("Invalid payload request.")

		response.JSON(w)
		return
	}

	// Create the thread (bearer token is extracted from context in the service)
	result, err := controller.OrchestratorServiceInterface.CreateThread(r.Context(), request)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.InvalidRequestPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid request payload."
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
		Message: "Thread created successfully.",
		Data:    result,
	}

	response.JSON(w)
}

// CreateMessage creates a new message
func (controller *OrchestratorController) CreateMessage(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")

	var request httpTypes.CreateMessageRequest
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

	// Validate minimal requirements
	if request.Message == "" {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Message is required.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	// Set the threadID from URL parameter
	serviceRequest := types.CreateMessageRequest{
		ThreadID: threadID,
		Content:  request.Message,
		Metadata: request.Metadata,
	}

	// Create the message (bearer token is extracted from context in the service)
	result, err := controller.OrchestratorServiceInterface.CreateMessage(r.Context(), serviceRequest)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.InvalidRequestPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid request payload."
		case "unauthorized": // Replace apiError.Unauthorized with string
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized."
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
		Message: "Message created successfully.",
		Data:    result,
	}

	response.JSON(w)
}

// UploadDicomPayload uploads a DICOM payload
func (controller *OrchestratorController) UploadDicomPayload(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")

	var request httpTypes.UploadDicomPayloadRequest
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

	// Validate required fields
	if request.StudyInstanceUID == "" {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Study Instance UID is required.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	if len(request.SeriesInstanceUIDs) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Series Instance UIDs are required.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	// Create the service request
	serviceRequest := types.DicomPayloadRequest{
		ThreadID:           threadID,
		StudyInstanceUID:   request.StudyInstanceUID,
		SeriesInstanceUIDs: request.SeriesInstanceUIDs,
		AdditionalMetadata: request.AdditionalMetadata,
		ContainerID:        request.ContainerID,
	}

	// Upload the DICOM payload (bearer token is extracted from context in the service)
	result, err := controller.OrchestratorServiceInterface.UploadDicomPayload(r.Context(), serviceRequest)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusInternalServerError,
			Success:   false,
			Message:   err.Error(),
			ErrorCode: "InternalServerError",
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully uploaded DICOM payload.",
		Data:    result,
	}

	response.JSON(w)
}

// GetThread gets thread information
func (controller *OrchestratorController) GetThread(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")

	if threadID == "" {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Thread ID is required.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// Get the thread (bearer token is extracted from context in the service)
	result, err := controller.OrchestratorServiceInterface.GetThread(r.Context(), threadID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.InvalidRequestPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid request payload."
		case "unauthorized": // Replace apiError.Unauthorized with string
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized."
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
		Message: "Thread information retrieved successfully.",
		Data:    result,
	}

	response.JSON(w)
}
