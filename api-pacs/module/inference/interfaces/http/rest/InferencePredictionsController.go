package rest

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// InferencePredictionsController serves the AI Predictions page. The tenant,
// user and role always come from the authenticated request context.
type InferencePredictionsController struct {
	application.InferencePredictionsServiceInterface
	// Now is used for the export file name; time.Now when nil.
	Now func() time.Time
}

type predictionsIdentity struct {
	tenantID string
	userID   string
	role     string
}

func predictionsRequestIdentity(r *http.Request) (predictionsIdentity, bool) {
	tenantID, ok := worklistTenantID(r)
	userID, userOK := r.Context().Value(iamTypes.UserIDCtx).(string)
	role, _ := r.Context().Value(iamTypes.RoleCtx).(string)
	userID = strings.TrimSpace(userID)
	if !ok || !userOK || userID == "" {
		return predictionsIdentity{}, false
	}
	return predictionsIdentity{tenantID: tenantID, userID: userID, role: strings.TrimSpace(role)}, true
}

func parsePredictionsTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func parsePredictionsRequest(r *http.Request, identity predictionsIdentity) (serviceTypes.ListPredictions, error) {
	values := r.URL.Query()
	from, err := parsePredictionsTime(values.Get("from"))
	if err != nil {
		return serviceTypes.ListPredictions{}, err
	}
	to, err := parsePredictionsTime(values.Get("to"))
	if err != nil {
		return serviceTypes.ListPredictions{}, err
	}
	limit, err := parseOptionalWorklistInteger(values.Get("limit"))
	if err != nil {
		return serviceTypes.ListPredictions{}, err
	}
	offset, err := parseOptionalWorklistInteger(values.Get("offset"))
	if err != nil {
		return serviceTypes.ListPredictions{}, err
	}
	return serviceTypes.ListPredictions{
		TenantID: identity.tenantID, UserID: identity.userID, Role: identity.role,
		View:   serviceTypes.PredictionsView(values.Get("view")),
		Search: values.Get("q"), Model: values.Get("model"), Status: values.Get("status"),
		Modality: values.Get("modality"), From: from, To: to, LaunchedBy: values.Get("launchedBy"),
		Limit: limit, Offset: offset,
	}, nil
}

func writePredictionsError(w http.ResponseWriter, errorCode string) {
	status, message := http.StatusInternalServerError, "Please contact technical support."
	switch errorCode {
	case apiError.InvalidPayload, apiError.InvalidRequestPayload:
		status, message = http.StatusBadRequest, "Invalid AI predictions query."
	case apiError.MaximumLimitReached:
		status, message = http.StatusBadRequest, "The page size exceeds the maximum."
	case apiError.UnauthorizedAccess:
		status, message = http.StatusUnauthorized, "Authentication is required."
	case apiError.ForbiddenAccess:
		status, message = http.StatusForbidden, "Only workspace administrators can see every run."
	case apiError.MissingRecord:
		status, message = http.StatusNotFound, "Not found."
	case apiError.DatabaseError:
		message = "Database error."
	case apiError.HystrixTimeout:
		status, message = http.StatusServiceUnavailable, "AI predictions are temporarily unavailable."
	default:
		errorCode = apiError.ServerError
	}
	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: errorCode}
	response.JSON(w)
}

// ListPredictions handles GET /v1/inference/predictions.
func (controller *InferencePredictionsController) ListPredictions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	identity, ok := predictionsRequestIdentity(r)
	if !ok {
		writePredictionsError(w, apiError.UnauthorizedAccess)
		return
	}
	request, err := parsePredictionsRequest(r, identity)
	if err != nil {
		writePredictionsError(w, apiError.InvalidPayload)
		return
	}
	page, err := controller.InferencePredictionsServiceInterface.ListPredictions(r.Context(), request)
	if err != nil {
		writePredictionsError(w, err.Error())
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true, Message: "Successfully retrieved AI predictions.", Data: page,
	}
	response.JSON(w)
}

// GetPredictionModelHistory handles GET /v1/inference/predictions/{studyInstanceUID}/history?model=.
func (controller *InferencePredictionsController) GetPredictionModelHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	identity, ok := predictionsRequestIdentity(r)
	if !ok {
		writePredictionsError(w, apiError.UnauthorizedAccess)
		return
	}
	limit, err := parseOptionalWorklistInteger(r.URL.Query().Get("limit"))
	if err != nil {
		writePredictionsError(w, apiError.InvalidPayload)
		return
	}
	offset, err := parseOptionalWorklistInteger(r.URL.Query().Get("offset"))
	if err != nil {
		writePredictionsError(w, apiError.InvalidPayload)
		return
	}
	studyInstanceUID, err := url.PathUnescape(chi.URLParam(r, "studyInstanceUID"))
	if err != nil {
		writePredictionsError(w, apiError.InvalidPayload)
		return
	}
	page, err := controller.InferencePredictionsServiceInterface.GetPredictionModelHistory(r.Context(), serviceTypes.GetPredictionModelHistory{
		TenantID: identity.tenantID, UserID: identity.userID, StudyInstanceUID: studyInstanceUID,
		Model: r.URL.Query().Get("model"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writePredictionsError(w, err.Error())
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true, Message: "Successfully retrieved the model history.", Data: page,
	}
	response.JSON(w)
}

// ExportPredictions handles GET /v1/inference/predictions/export.csv: one
// line per model run of the view and filters, streamed as UTF-8 CSV.
// X-Export-Truncated is "true" when the selection exceeded the export bound.
func (controller *InferencePredictionsController) ExportPredictions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	identity, ok := predictionsRequestIdentity(r)
	if !ok {
		writePredictionsError(w, apiError.UnauthorizedAccess)
		return
	}
	request, err := parsePredictionsRequest(r, identity)
	if err != nil {
		writePredictionsError(w, apiError.InvalidPayload)
		return
	}
	now := time.Now
	if controller.Now != nil {
		now = controller.Now
	}
	started := false
	start := func(truncated bool) io.Writer {
		started = true
		header := w.Header()
		header.Set("Content-Type", "text/csv; charset=utf-8")
		header.Set("Content-Disposition", `attachment; filename="ai-predictions-`+now().UTC().Format("20060102-150405")+`.csv"`)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Export-Truncated", strconv.FormatBool(truncated))
		header.Set("Access-Control-Expose-Headers", "Content-Disposition, X-Export-Truncated")
		w.WriteHeader(http.StatusOK)
		// Byte order mark so spreadsheet applications read UTF-8.
		_, _ = io.WriteString(w, "\xEF\xBB\xBF")
		return w
	}
	summary, err := controller.InferencePredictionsServiceInterface.ExportPredictions(r.Context(), request, start)
	if err != nil {
		if !started {
			writePredictionsError(w, err.Error())
			return
		}
		if !errors.Is(err, r.Context().Err()) || r.Context().Err() == nil {
			log.Printf("[AI predictions] export stopped after %d rows err=%v", summary.Rows, err)
		}
	}
}
