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
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

const runLabelMaxBodyBytes = 4 << 10

// AddRunLabelRequest is the body of POST .../jobs/{studyServiceJobId}/labels.
type AddRunLabelRequest struct {
	Label string `json:"label"`
}

// runLabelTarget reads the tenant, the run and the caller's model access.
func runLabelTarget(w http.ResponseWriter, r *http.Request) (serviceTypes.GetRunLabels, bool) {
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return serviceTypes.GetRunLabels{}, false
	}
	allowModel, accessErr := resultModelFilter(r)
	if accessErr != "" {
		writeModelAccessError(w, accessErr)
		return serviceTypes.GetRunLabels{}, false
	}
	return serviceTypes.GetRunLabels{
		TenantID:          tenantID,
		StudyInstanceUID:  chi.URLParam(r, "studyInstanceUID"),
		StudyServiceJobID: chi.URLParam(r, "studyServiceJobId"),
		AllowModel:        allowModel,
	}, true
}

func writeRunLabelError(w http.ResponseWriter, err error) {
	if err.Error() == apiError.ForbiddenAccess {
		writeModelAccessError(w, apiError.ForbiddenAccess)
		return
	}
	writeViewerResultsError(w, err.Error())
}

func runLabelUserID(r *http.Request) (string, bool) {
	userID, ok := r.Context().Value(iamTypes.UserIDCtx).(string)
	userID = strings.TrimSpace(userID)
	return userID, ok && userID != ""
}

// GetStudyRunLabels returns the labels of one run (study-service job) of a
// study. Readable by every user who can read the run's result.
func (controller *InferenceQueryController) GetStudyRunLabels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	target, ok := runLabelTarget(w, r)
	if !ok {
		return
	}
	labels, err := controller.InferenceQueryServiceInterface.GetStudyRunLabels(r.Context(), target)
	if err != nil {
		writeRunLabelError(w, err)
		return
	}
	writeWorklistQuerySuccess(w, "Successfully retrieved run labels.", labels)
}

// AddStudyRunLabel puts a free-text label on one run. Mounted behind
// models.use; the run's model must be one the caller may use.
func (controller *InferenceQueryController) AddStudyRunLabel(w http.ResponseWriter, r *http.Request) {
	target, ok := runLabelTarget(w, r)
	if !ok {
		return
	}
	userID, ok := runLabelUserID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	var request AddRunLabelRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, runLabelMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeViewerResultsError(w, apiError.InvalidPayload)
		return
	}
	labels, added, err := controller.InferenceQueryServiceInterface.AddStudyRunLabel(r.Context(), serviceTypes.ChangeRunLabel{
		GetRunLabels: target, UserID: userID, Label: request.Label,
	})
	if err != nil {
		writeRunLabelError(w, err)
		return
	}
	status, message := http.StatusOK, "The run already carries this label."
	if added {
		status, message = http.StatusCreated, "Successfully added run label."
	}
	response := viewmodels.HTTPResponseVM{Status: status, Success: true, Message: message, Data: labels}
	response.JSON(w)
}

// RemoveStudyRunLabel takes the label given in ?label= off one run. Mounted
// behind models.use; the run's model must be one the caller may use.
func (controller *InferenceQueryController) RemoveStudyRunLabel(w http.ResponseWriter, r *http.Request) {
	target, ok := runLabelTarget(w, r)
	if !ok {
		return
	}
	userID, ok := runLabelUserID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	labels, err := controller.InferenceQueryServiceInterface.RemoveStudyRunLabel(r.Context(), serviceTypes.ChangeRunLabel{
		GetRunLabels: target, UserID: userID, Label: r.URL.Query().Get("label"),
	})
	if err != nil {
		writeRunLabelError(w, err)
		return
	}
	writeWorklistQuerySuccess(w, "Successfully removed run label.", labels)
}

// GetRunLabelCatalog lists the labels the tenant has used, for suggestions.
func (controller *InferenceQueryController) GetRunLabelCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeViewerResultsError(w, apiError.UnauthorizedAccess)
		return
	}
	catalog, err := controller.InferenceQueryServiceInterface.GetRunLabelCatalog(r.Context(), tenantID)
	if err != nil {
		writeRunLabelError(w, err)
		return
	}
	writeWorklistQuerySuccess(w, "Successfully retrieved run labels.", catalog)
}
