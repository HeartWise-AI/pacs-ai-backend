package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type predictionsServiceFake struct {
	listInputs    []serviceTypes.ListPredictions
	historyInputs []serviceTypes.GetPredictionModelHistory
	exportInputs  []serviceTypes.ListPredictions
	page          serviceTypes.PredictionsPage
	history       serviceTypes.PredictionHistoryPage
	err           error
	exportBody    string
	exportErr     error
	truncated     bool
	countInputs   []serviceTypes.ListFeedbackReportCounts
	counts        serviceTypes.FeedbackReportCounts
}

func (fake *predictionsServiceFake) ListFeedbackReportCounts(_ context.Context, data serviceTypes.ListFeedbackReportCounts) (serviceTypes.FeedbackReportCounts, error) {
	fake.countInputs = append(fake.countInputs, data)
	return fake.counts, fake.err
}

func (fake *predictionsServiceFake) ListPredictions(_ context.Context, data serviceTypes.ListPredictions) (serviceTypes.PredictionsPage, error) {
	fake.listInputs = append(fake.listInputs, data)
	return fake.page, fake.err
}

func (fake *predictionsServiceFake) GetPredictionModelHistory(_ context.Context, data serviceTypes.GetPredictionModelHistory) (serviceTypes.PredictionHistoryPage, error) {
	fake.historyInputs = append(fake.historyInputs, data)
	return fake.history, fake.err
}

func (fake *predictionsServiceFake) ExportPredictions(_ context.Context, data serviceTypes.ListPredictions, start func(bool) io.Writer) (serviceTypes.PredictionsExportSummary, error) {
	fake.exportInputs = append(fake.exportInputs, data)
	if fake.err != nil {
		return serviceTypes.PredictionsExportSummary{}, fake.err
	}
	_, _ = io.WriteString(start(fake.truncated), fake.exportBody)
	return serviceTypes.PredictionsExportSummary{Rows: 1}, fake.exportErr
}

func predictionsRequest(target string, identity map[interface{}]interface{}, params map[string]string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	routeContext := chi.NewRouteContext()
	for key, value := range params {
		routeContext.URLParams.Add(key, value)
	}
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	for key, value := range identity {
		ctx = context.WithValue(ctx, key, value)
	}
	return request.WithContext(ctx)
}

func predictionsIdentityFor(role string) map[interface{}]interface{} {
	return map[interface{}]interface{}{
		iamTypes.TenantIDCtx: "tenant-a", iamTypes.UserIDCtx: "user-a", iamTypes.RoleCtx: role,
	}
}

func decodePredictionsBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestListPredictionsPassesTheSessionIdentityAndTheFilters(t *testing.T) {
	service := &predictionsServiceFake{page: serviceTypes.PredictionsPage{
		View: serviceTypes.PredictionsViewAll, Exams: []serviceTypes.PredictionExam{}, Limit: 25, Total: 0, CanViewAll: true,
	}}
	controller := InferencePredictionsController{InferencePredictionsServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.ListPredictions(recorder, predictionsRequest(
		"/v1/inference/predictions?view=all&q=ACC&model=PanEcho&status=active&modality=US"+
			"&from=2026-11-09T00:00:00%2B01:00&to=2026-11-16T00:00:00Z&launchedBy=user:user-b&limit=10&offset=20"+
			"&tenantId=tenant-b&userId=user-b&role=OWNER",
		predictionsIdentityFor("ADMIN"), nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	from := time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	require.Equal(t, []serviceTypes.ListPredictions{{
		TenantID: "tenant-a", UserID: "user-a", Role: "ADMIN", CanViewAll: true, View: serviceTypes.PredictionsViewAll,
		Search: "ACC", Model: "PanEcho", Status: "active", Modality: "US", From: &from, To: &to,
		LaunchedBy: "user:user-b", Limit: 10, Offset: 20,
	}}, service.listInputs)
	body := decodePredictionsBody(t, recorder)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]interface{})
	require.Equal(t, "all", data["view"])
	require.Equal(t, true, data["canViewAll"])
	require.Equal(t, []interface{}{}, data["exams"])
}

func TestListPredictionsMapsErrors(t *testing.T) {
	cases := map[string]int{
		apiError.ForbiddenAccess:     http.StatusForbidden,
		apiError.InvalidPayload:      http.StatusBadRequest,
		apiError.MaximumLimitReached: http.StatusBadRequest,
		apiError.DatabaseError:       http.StatusInternalServerError,
		"something unexpected":       http.StatusInternalServerError,
	}
	for code, status := range cases {
		service := &predictionsServiceFake{err: errors.New(code)}
		controller := InferencePredictionsController{InferencePredictionsServiceInterface: service}
		recorder := httptest.NewRecorder()
		controller.ListPredictions(recorder, predictionsRequest("/v1/inference/predictions?view=all", predictionsIdentityFor("USER"), nil))
		require.Equal(t, status, recorder.Code, code)
		require.NotContains(t, recorder.Body.String(), "something unexpected")
	}

	for _, target := range []string{
		"/v1/inference/predictions?from=yesterday",
		"/v1/inference/predictions?to=2026-11-16",
		"/v1/inference/predictions?limit=ten",
		"/v1/inference/predictions?offset=-x",
	} {
		service := &predictionsServiceFake{}
		recorder := httptest.NewRecorder()
		(&InferencePredictionsController{InferencePredictionsServiceInterface: service}).ListPredictions(
			recorder, predictionsRequest(target, predictionsIdentityFor("USER"), nil))
		require.Equal(t, http.StatusBadRequest, recorder.Code, target)
		require.Empty(t, service.listInputs)
	}
}

func TestPredictionsRequireTenantAndUser(t *testing.T) {
	for name, identity := range map[string]map[interface{}]interface{}{
		"none":       {},
		"no tenant":  {iamTypes.UserIDCtx: "user-a"},
		"blank user": {iamTypes.TenantIDCtx: "tenant-a", iamTypes.UserIDCtx: " "},
	} {
		service := &predictionsServiceFake{}
		controller := InferencePredictionsController{InferencePredictionsServiceInterface: service}
		for _, handler := range []http.HandlerFunc{controller.ListPredictions, controller.ExportPredictions, controller.GetPredictionModelHistory} {
			recorder := httptest.NewRecorder()
			handler(recorder, predictionsRequest("/v1/inference/predictions?view=mine", identity, map[string]string{"studyInstanceUID": "1.2"}))
			require.Equal(t, http.StatusUnauthorized, recorder.Code, name)
		}
		require.Empty(t, service.listInputs)
		require.Empty(t, service.exportInputs)
		require.Empty(t, service.historyInputs)
	}
}

func TestGetPredictionModelHistoryUsesThePathAndQuery(t *testing.T) {
	service := &predictionsServiceFake{history: serviceTypes.PredictionHistoryPage{
		StudyInstanceUID: "1.2.3", ModelName: "DeepCORO-JEPA", Executions: []serviceTypes.PredictionExecution{},
	}}
	controller := InferencePredictionsController{InferencePredictionsServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.GetPredictionModelHistory(recorder, predictionsRequest(
		"/v1/inference/predictions/1.2.3/history?model=DeepCORO-JEPA&limit=5&offset=5",
		predictionsIdentityFor("USER"), map[string]string{"studyInstanceUID": "1.2.3"}))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []serviceTypes.GetPredictionModelHistory{{
		TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: "1.2.3", Model: "DeepCORO-JEPA", Limit: 5, Offset: 5,
	}}, service.historyInputs)
	data := decodePredictionsBody(t, recorder)["data"].(map[string]interface{})
	require.Equal(t, "DeepCORO-JEPA", data["modelName"])
}

func TestExportPredictionsStreamsCSV(t *testing.T) {
	service := &predictionsServiceFake{exportBody: "Run time,Study Instance UID\r\n", truncated: true}
	controller := InferencePredictionsController{
		InferencePredictionsServiceInterface: service,
		Now:                                  func() time.Time { return time.Date(2026, 11, 16, 9, 30, 5, 0, time.UTC) },
	}
	recorder := httptest.NewRecorder()
	controller.ExportPredictions(recorder, predictionsRequest(
		"/v1/inference/predictions/export.csv?view=automatic&status=attention", predictionsIdentityFor("USER"), nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/csv; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="ai-predictions-20261116-093005.csv"`, recorder.Header().Get("Content-Disposition"))
	require.Equal(t, "true", recorder.Header().Get("X-Export-Truncated"))
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	require.True(t, strings.HasPrefix(recorder.Body.String(), "\xEF\xBB\xBFRun time,"))
	require.Equal(t, serviceTypes.PredictionsViewAutomatic, service.exportInputs[0].View)
	require.Equal(t, "tenant-a", service.exportInputs[0].TenantID)
	require.Equal(t, "attention", service.exportInputs[0].Status)
}

func TestExportPredictionsRefusalIsJSON(t *testing.T) {
	service := &predictionsServiceFake{err: errors.New(apiError.ForbiddenAccess)}
	controller := InferencePredictionsController{InferencePredictionsServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.ExportPredictions(recorder, predictionsRequest(
		"/v1/inference/predictions/export.csv?view=all", predictionsIdentityFor("USER"), nil))
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotEqual(t, "text/csv; charset=utf-8", recorder.Header().Get("Content-Type"))
	require.Equal(t, apiError.ForbiddenAccess, decodePredictionsBody(t, recorder)["errorCode"])
}

func TestPredictionsOpenAPIDocumentsTheRoutes(t *testing.T) {
	paths := openAPIMap(t, loadWorklistOpenAPI(t)["paths"])
	for _, path := range []string{
		"/inference/predictions",
		"/inference/predictions/export.csv",
		"/inference/predictions/{studyInstanceUID}/history",
	} {
		operation := openAPIMap(t, openAPIMap(t, paths[path])["get"])
		responses := openAPIMap(t, operation["responses"])
		require.Contains(t, responses, "200", path)
		require.Contains(t, responses, "403", path)
	}
	list := openAPIMap(t, openAPIMap(t, paths["/inference/predictions"])["get"])
	require.Contains(t, list["description"], "view=all requires ADMIN or OWNER")
}
