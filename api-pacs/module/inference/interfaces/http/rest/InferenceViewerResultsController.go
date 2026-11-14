package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

const viewerModelRunMaxBodyBytes = 256 << 10

// ViewerModelRunRequest is the optional body of POST .../models/{modelId}/run.
type ViewerModelRunRequest struct {
	SeriesInstanceUIDs []string `json:"seriesInstanceUIDs"`
	Modality           string   `json:"modality"`
}

// ViewerModelRunResponse is returned with 201 after a viewer run is created.
type ViewerModelRunResponse struct {
	RunID        string                                        `json:"runId"`
	RunNumber    int                                           `json:"runNumber"`
	Trigger      entity.InferenceIngestionProcessingRunTrigger `json:"trigger"`
	Phase        entity.InferenceIngestionProcessingRunPhase   `json:"phase"`
	ExecutionID  string                                        `json:"executionId"`
	ModelID      string                                        `json:"modelId"`
	ModelName    string                                        `json:"modelName"`
	ModelVersion string                                        `json:"modelVersion"`
}

// GetStudyLatestModelResults returns the latest completed result per model for
// one study in the authenticated tenant, joined with registrations and runs.
func (controller *InferenceQueryController) GetStudyLatestModelResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	results, err := controller.InferenceQueryServiceInterface.GetStudyLatestModelResults(r.Context(), serviceTypes.GetStudyLatestModelResults{
		TenantID:         tenantID,
		StudyInstanceUID: chi.URLParam(r, "studyInstanceUID"),
		Modality:         r.URL.Query().Get("modality"),
	})
	if err != nil {
		writeViewerResultsError(w, err.Error())
		return
	}
	writeWorklistQuerySuccess(w, "Successfully retrieved latest study model results.", results)
}

// GetProcessingRunExecutionReport re-renders the HTML report of one completed
// execution in the authenticated tenant.
func (controller *InferenceQueryController) GetProcessingRunExecutionReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	report, err := controller.InferenceQueryServiceInterface.GetProcessingRunExecutionReport(r.Context(), serviceTypes.GetProcessingRunExecutionReport{
		TenantID: tenantID, RunID: chi.URLParam(r, "runId"), ExecutionID: chi.URLParam(r, "executionId"),
	})
	if err != nil {
		writeViewerResultsError(w, err.Error())
		return
	}
	writeWorklistQuerySuccess(w, "Successfully rendered model report.", report)
}

// GetStudyJobReport re-renders the HTML report of a legacy study-service job
// (no run/execution IDs) that belongs to the study and tenant.
func (controller *InferenceQueryController) GetStudyJobReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	report, err := controller.InferenceQueryServiceInterface.GetStudyJobReport(r.Context(), serviceTypes.GetStudyJobReport{
		TenantID:          tenantID,
		StudyInstanceUID:  chi.URLParam(r, "studyInstanceUID"),
		StudyServiceJobID: chi.URLParam(r, "studyServiceJobId"),
	})
	if err != nil {
		writeViewerResultsError(w, err.Error())
		return
	}
	writeWorklistQuerySuccess(w, "Successfully rendered model report.", report)
}

// RunStudyModel creates a single-model VIEWER_REQUEST run for any
// authenticated tenant user, reserving quota like interactive predict.
func (controller *InferenceCommandController) RunStudyModel(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := worklistTenantID(r)
	userID, userOK := r.Context().Value(iamTypes.UserIDCtx).(string)
	if !ok || !userOK || strings.TrimSpace(userID) == "" {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}

	var request ViewerModelRunRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, viewerModelRunMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeViewerResultsError(w, apiError.InvalidPayload)
		return
	}

	result, err := controller.InferenceCommandServiceInterface.CreateViewerModelRun(r.Context(), serviceTypes.CreateViewerModelRun{
		TenantID:           tenantID,
		UserID:             userID,
		StudyInstanceUID:   chi.URLParam(r, "studyInstanceUID"),
		ModelRef:           chi.URLParam(r, "modelId"),
		SeriesInstanceUIDs: request.SeriesInstanceUIDs,
		Modality:           request.Modality,
	})
	if err != nil {
		if writeInferenceQuotaError(w, err) {
			return
		}
		writeViewerResultsError(w, err.Error())
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully created viewer model run.",
		Data: &ViewerModelRunResponse{
			RunID:        result.Run.ID,
			RunNumber:    result.Run.RunNumber,
			Trigger:      result.Run.RunTrigger,
			Phase:        result.Run.Phase,
			ExecutionID:  result.Execution.ID,
			ModelID:      result.ModelID,
			ModelName:    result.ModelName,
			ModelVersion: result.ModelVersion,
		},
	}
	response.JSON(w)
}

func writeViewerResultsError(w http.ResponseWriter, serviceError string) {
	status := http.StatusInternalServerError
	message := "Please contact technical support."
	errorCode := serviceError

	switch serviceError {
	case apiError.InvalidPayload, apiError.InvalidRequestPayload:
		status, message, errorCode = http.StatusBadRequest, "Invalid request.", apiError.InvalidPayload
	case apiError.UnauthorizedAccess:
		status, message = http.StatusUnauthorized, "Authentication is required."
	case apiError.ForbiddenAccess:
		status, message = http.StatusForbidden, "Access is forbidden."
	case apiError.MissingRecord:
		status, message = http.StatusNotFound, "Not found."
	case apiError.ReportNotAvailable:
		status, message = http.StatusNotFound, "This model cannot render a report for this result."
	case apiError.InferenceExecutionResultNotAvailable:
		status, message = http.StatusConflict, "This result is not completed."
	case apiError.RunAlreadyActive:
		status, message = http.StatusConflict, "This model is already queued or running for this study."
	case apiError.StudyNotAvailableLocally:
		status, message = http.StatusConflict, "The study is not stored locally."
	case apiError.InferenceExecutionResultInvalid:
		status, message = http.StatusUnprocessableEntity, "The completed model result is unavailable."
	case apiError.ReportRenderRejected:
		status, message = http.StatusUnprocessableEntity, "The model could not render a report from this result."
	case apiError.ReportRenderFailed:
		status, message = http.StatusBadGateway, "Report rendering failed."
	case apiError.InferenceResultServiceUnavailable, apiError.HystrixTimeout:
		status, message, errorCode = http.StatusServiceUnavailable, "Model results are temporarily unavailable.", apiError.InferenceResultServiceUnavailable
	case apiError.InferenceModelUnavailable:
		status, message = http.StatusServiceUnavailable, "The model is temporarily unavailable."
	case apiError.InferenceQuotaUnavailable:
		status, message = http.StatusServiceUnavailable, "Inference quota enforcement is temporarily unavailable."
	case apiError.DatabaseError, apiError.FirestoreError:
		message = "Database error."
	default:
		errorCode = apiError.ServerError
	}

	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: errorCode}
	response.JSON(w)
}
