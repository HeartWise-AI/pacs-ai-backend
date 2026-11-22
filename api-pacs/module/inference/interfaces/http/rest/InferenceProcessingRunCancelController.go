package rest

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// CancelProcessingRun cancels the models of one run of the authenticated
// tenant that have not started computing, and returns the run detail. A run
// that already ended, or was already cancelled, returns its current state.
func (controller *InferenceCommandController) CancelProcessingRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	userID, userOK := r.Context().Value(iamTypes.UserIDCtx).(string)
	if !ok || !userOK || strings.TrimSpace(userID) == "" {
		writeWorklistQueryError(w, apiError.UnauthorizedAccess)
		return
	}

	detail, err := controller.InferenceCommandServiceInterface.CancelProcessingRun(r.Context(), serviceTypes.CancelProcessingRun{
		TenantID: tenantID, RunID: chi.URLParam(r, "runId"), UserID: userID,
	})
	if err != nil {
		if err.Error() == apiError.ProcessingRunNotCancellable {
			response := viewmodels.HTTPResponseVM{
				Status: http.StatusConflict, Success: false, ErrorCode: apiError.ProcessingRunNotCancellable,
				Message: "Every remaining model of this run is already computing.",
			}
			response.JSON(w)
			return
		}
		writeWorklistQueryError(w, err.Error())
		return
	}

	writeWorklistQuerySuccess(w, "Successfully cancelled the waiting models of the processing run.", detail)
}
