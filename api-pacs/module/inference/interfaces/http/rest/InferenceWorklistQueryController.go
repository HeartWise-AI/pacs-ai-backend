package rest

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// GetWorklistStudyStatuses returns the current status snapshot for studies in
// the authenticated tenant. studyInstanceUID is a repeatable query parameter.
func (controller *InferenceQueryController) GetWorklistStudyStatuses(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeWorklistQueryError(w, apiError.UnauthorizedAccess)
		return
	}

	limit, offset, err := parseWorklistPagination(r)
	if err != nil {
		writeWorklistQueryError(w, apiError.InvalidPayload)
		return
	}

	page, err := controller.InferenceQueryServiceInterface.GetWorklistStudyStatuses(r.Context(), serviceTypes.GetWorklistStudyStatuses{
		TenantID:          tenantID,
		StudyInstanceUIDs: r.URL.Query()["studyInstanceUID"],
		Limit:             limit,
		Offset:            offset,
	})
	if err != nil {
		writeWorklistQueryError(w, err.Error())
		return
	}

	writeWorklistQuerySuccess(w, "Successfully retrieved worklist study statuses.", page)
}

// GetStudyProcessingRunHistory returns newest-first processing attempts and
// their model executions for one study in the authenticated tenant.
func (controller *InferenceQueryController) GetStudyProcessingRunHistory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeWorklistQueryError(w, apiError.UnauthorizedAccess)
		return
	}

	limit, offset, err := parseWorklistPagination(r)
	if err != nil {
		writeWorklistQueryError(w, apiError.InvalidPayload)
		return
	}

	page, err := controller.InferenceQueryServiceInterface.GetStudyProcessingRunHistory(r.Context(), serviceTypes.GetStudyProcessingRunHistory{
		TenantID: tenantID, StudyInstanceUID: chi.URLParam(r, "studyInstanceUID"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeWorklistQueryError(w, err.Error())
		return
	}

	writeWorklistQuerySuccess(w, "Successfully retrieved study processing-run history.", page)
}

// GetProcessingRunDetail returns one processing run and its frozen model plan
// only when it belongs to the authenticated tenant.
func (controller *InferenceQueryController) GetProcessingRunDetail(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeWorklistQueryError(w, apiError.UnauthorizedAccess)
		return
	}

	detail, err := controller.InferenceQueryServiceInterface.GetProcessingRunDetail(r.Context(), serviceTypes.GetProcessingRunDetail{
		TenantID: tenantID, RunID: chi.URLParam(r, "runId"),
	})
	if err != nil {
		writeWorklistQueryError(w, err.Error())
		return
	}

	writeWorklistQuerySuccess(w, "Successfully retrieved processing-run detail.", detail)
}

// GetProcessingRunExecutionResult lazily returns one completed result scoped
// by the authenticated tenant and both stable path identifiers.
func (controller *InferenceQueryController) GetProcessingRunExecutionResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeExecutionResultError(w, apiError.UnauthorizedAccess)
		return
	}

	result, err := controller.InferenceQueryServiceInterface.GetProcessingRunExecutionResult(r.Context(), serviceTypes.GetProcessingRunExecutionResult{
		TenantID: tenantID, RunID: chi.URLParam(r, "runId"), ExecutionID: chi.URLParam(r, "executionId"),
	})
	if err != nil {
		writeExecutionResultError(w, err.Error())
		return
	}

	writeWorklistQuerySuccess(w, "Successfully retrieved model execution result.", result)
}

func worklistTenantID(r *http.Request) (string, bool) {
	value := r.Context().Value(iamTypes.TenantIDCtx)
	tenantID, ok := value.(string)
	tenantID = strings.TrimSpace(tenantID)
	return tenantID, ok && tenantID != ""
}

func parseWorklistPagination(r *http.Request) (int, int, error) {
	limit, err := parseOptionalWorklistInteger(r.URL.Query().Get("limit"))
	if err != nil {
		return 0, 0, err
	}
	offset, err := parseOptionalWorklistInteger(r.URL.Query().Get("offset"))
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

func parseOptionalWorklistInteger(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.Atoi(value)
}

func writeWorklistQuerySuccess(w http.ResponseWriter, message string, data interface{}) {
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true, Message: message, Data: data,
	}
	response.JSON(w)
}

func writeWorklistQueryError(w http.ResponseWriter, errorCode string) {
	status := http.StatusInternalServerError
	message := "Please contact technical support."

	switch errorCode {
	case apiError.InvalidPayload, apiError.InvalidRequestPayload:
		status = http.StatusBadRequest
		message = "Invalid worklist query."
	case apiError.MaximumLimitReached:
		status = http.StatusBadRequest
		message = "Worklist page limit exceeds the maximum."
	case apiError.UnauthorizedAccess:
		status = http.StatusUnauthorized
		message = "Authentication is required."
	case apiError.ForbiddenAccess:
		status = http.StatusForbidden
		message = "Access is forbidden."
	case apiError.MissingRecord:
		status = http.StatusNotFound
		message = "Processing run not found."
	case apiError.DatabaseError:
		message = "Database error."
	case apiError.HystrixTimeout:
		status = http.StatusServiceUnavailable
		message = "Worklist status is temporarily unavailable."
	}

	response := viewmodels.HTTPResponseVM{
		Status: status, Success: false, Message: message, ErrorCode: errorCode,
	}
	response.JSON(w)
}

func writeExecutionResultError(w http.ResponseWriter, serviceError string) {
	status := http.StatusInternalServerError
	message := "Model result retrieval failed."
	errorCode := apiError.ServerError

	switch serviceError {
	case apiError.InvalidPayload, apiError.InvalidRequestPayload:
		status, message, errorCode = http.StatusBadRequest, "Invalid model result request.", apiError.InvalidPayload
	case apiError.UnauthorizedAccess:
		status, message, errorCode = http.StatusUnauthorized, "Authentication is required.", serviceError
	case apiError.ForbiddenAccess:
		status, message, errorCode = http.StatusForbidden, "Access is forbidden.", serviceError
	case apiError.MissingRecord:
		status, message, errorCode = http.StatusNotFound, "Model execution result not found.", serviceError
	case apiError.InferenceExecutionResultNotAvailable:
		status, message, errorCode = http.StatusConflict, "This model execution does not have a viewable completed result.", serviceError
	case apiError.InferenceExecutionResultInvalid:
		status, message, errorCode = http.StatusUnprocessableEntity, "The completed model result is unavailable.", serviceError
	case apiError.InferenceResultServiceUnavailable, apiError.HystrixTimeout:
		status, message, errorCode = http.StatusServiceUnavailable, "Model results are temporarily unavailable.", apiError.InferenceResultServiceUnavailable
	case apiError.DatabaseError:
		errorCode = serviceError
	}

	response := viewmodels.HTTPResponseVM{
		Status: status, Success: false, Message: message, ErrorCode: errorCode,
	}
	response.JSON(w)
}
