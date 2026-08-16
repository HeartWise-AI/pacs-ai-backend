package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type worklistQueryService struct {
	application.InferenceQueryServiceInterface
	statusInput  serviceTypes.GetWorklistStudyStatuses
	historyInput serviceTypes.GetStudyProcessingRunHistory
	detailInput  serviceTypes.GetProcessingRunDetail
	resultInput  serviceTypes.GetProcessingRunExecutionResult
	statusPage   serviceTypes.WorklistStudyStatusPage
	historyPage  serviceTypes.StudyProcessingRunHistoryPage
	detail       serviceTypes.ProcessingRunDetail
	result       serviceTypes.ProcessingRunExecutionResult
	err          error
}

func (service *worklistQueryService) GetWorklistStudyStatuses(_ context.Context, data serviceTypes.GetWorklistStudyStatuses) (serviceTypes.WorklistStudyStatusPage, error) {
	service.statusInput = data
	return service.statusPage, service.err
}

func (service *worklistQueryService) GetStudyProcessingRunHistory(_ context.Context, data serviceTypes.GetStudyProcessingRunHistory) (serviceTypes.StudyProcessingRunHistoryPage, error) {
	service.historyInput = data
	return service.historyPage, service.err
}

func (service *worklistQueryService) GetProcessingRunDetail(_ context.Context, data serviceTypes.GetProcessingRunDetail) (serviceTypes.ProcessingRunDetail, error) {
	service.detailInput = data
	return service.detail, service.err
}

func (service *worklistQueryService) GetProcessingRunExecutionResult(_ context.Context, data serviceTypes.GetProcessingRunExecutionResult) (serviceTypes.ProcessingRunExecutionResult, error) {
	service.resultInput = data
	return service.result, service.err
}

func worklistQueryRequest(method, target, tenantID string, params map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	routeContext := chi.NewRouteContext()
	for key, value := range params {
		routeContext.URLParams.Add(key, value)
	}
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	if tenantID != "" {
		ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, tenantID)
	}
	return request.WithContext(ctx)
}

func decodeWorklistResponse(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	Success   bool            `json:"success"`
	ErrorCode string          `json:"errorCode"`
	Data      json.RawMessage `json:"data"`
} {
	t.Helper()
	var response struct {
		Success   bool            `json:"success"`
		ErrorCode string          `json:"errorCode"`
		Data      json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestGetWorklistStudyStatusesUsesAuthenticatedTenantAndVisibleUIDs(t *testing.T) {
	service := &worklistQueryService{statusPage: serviceTypes.WorklistStudyStatusPage{
		Studies:      []serviceTypes.WorklistStudyStatus{},
		WorklistPage: serviceTypes.WorklistPage{Limit: 2, Offset: 4, HasMore: true},
	}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()
	request := worklistQueryRequest(
		http.MethodGet,
		"/v1/inference/worklist/status?tenantId=tenant-b&studyInstanceUID=1.2.3&studyInstanceUID=4.5.6&limit=2&offset=4",
		"tenant-a",
		nil,
	)

	controller.GetWorklistStudyStatuses(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, serviceTypes.GetWorklistStudyStatuses{
		TenantID: "tenant-a", StudyInstanceUIDs: []string{"1.2.3", "4.5.6"}, Limit: 2, Offset: 4,
	}, service.statusInput)
	response := decodeWorklistResponse(t, recorder)
	require.True(t, response.Success)
	require.NotContains(t, string(response.Data), "tenant-a")
	require.NotContains(t, string(response.Data), "tenant-b")
}

func TestGetStudyProcessingRunHistoryUsesPathStudyAndAuthenticatedTenant(t *testing.T) {
	service := &worklistQueryService{historyPage: serviceTypes.StudyProcessingRunHistoryPage{
		Runs: []serviceTypes.ProcessingRunDetail{}, WorklistPage: serviceTypes.WorklistPage{Limit: 25},
	}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()
	request := worklistQueryRequest(http.MethodGet, "/v1/inference/worklist/studies/1.2.3/runs", "tenant-a", map[string]string{
		"studyInstanceUID": "1.2.3",
	})

	controller.GetStudyProcessingRunHistory(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, serviceTypes.GetStudyProcessingRunHistory{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	}, service.historyInput)
}

func TestGetProcessingRunDetailUsesPathRunAndAuthenticatedTenant(t *testing.T) {
	service := &worklistQueryService{detail: serviceTypes.ProcessingRunDetail{
		ProcessingRunSummary: serviceTypes.ProcessingRunSummary{RunID: "run-7"},
		Executions:           []serviceTypes.ProcessingRunExecutionSummary{},
	}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()
	request := worklistQueryRequest(http.MethodGet, "/v1/inference/processing/runs/run-7", "tenant-a", map[string]string{
		"runId": "run-7",
	})

	controller.GetProcessingRunDetail(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, serviceTypes.GetProcessingRunDetail{TenantID: "tenant-a", RunID: "run-7"}, service.detailInput)
}

func TestGetProcessingRunExecutionResultUsesAuthenticatedTenantAndBothPathIDs(t *testing.T) {
	service := &worklistQueryService{result: serviceTypes.ProcessingRunExecutionResult{
		RunID: "run-7", ExecutionID: "execution-9", StudyInstanceUID: "1.2.3",
		Result: json.RawMessage(`{"score":24.5}`),
	}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()
	request := worklistQueryRequest(
		http.MethodGet,
		"/v1/inference/processing/runs/run-7/executions/execution-9/result?tenantId=tenant-b",
		"tenant-a",
		map[string]string{"runId": "run-7", "executionId": "execution-9"},
	)

	controller.GetProcessingRunExecutionResult(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, serviceTypes.GetProcessingRunExecutionResult{
		TenantID: "tenant-a", RunID: "run-7", ExecutionID: "execution-9",
	}, service.resultInput)
	response := decodeWorklistResponse(t, recorder)
	require.True(t, response.Success)
	require.Contains(t, string(response.Data), `"score":24.5`)
	for _, forbidden := range []string{"tenant-a", "tenant-b", "studyServiceJobId", "patientId", "operatorToken"} {
		require.NotContains(t, string(response.Data), forbidden)
	}
}

func TestGetProcessingRunExecutionResultRejectsMissingAuthenticationWithoutCallingService(t *testing.T) {
	service := &worklistQueryService{}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetProcessingRunExecutionResult(recorder, worklistQueryRequest(
		http.MethodGet, "/v1/inference/processing/runs/run-1/executions/execution-1/result", "",
		map[string]string{"runId": "run-1", "executionId": "execution-1"},
	))

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Empty(t, service.resultInput)
}

func TestGetProcessingRunExecutionResultMapsOnlyStableSafeErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        string
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: apiError.InvalidPayload, wantStatus: http.StatusBadRequest, wantCode: apiError.InvalidPayload},
		{name: "missing", err: apiError.MissingRecord, wantStatus: http.StatusNotFound, wantCode: apiError.MissingRecord},
		{name: "not available", err: apiError.InferenceExecutionResultNotAvailable, wantStatus: http.StatusConflict, wantCode: apiError.InferenceExecutionResultNotAvailable},
		{name: "invalid result", err: apiError.InferenceExecutionResultInvalid, wantStatus: http.StatusUnprocessableEntity, wantCode: apiError.InferenceExecutionResultInvalid},
		{name: "upstream unavailable", err: apiError.InferenceResultServiceUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: apiError.InferenceResultServiceUnavailable},
		{name: "unknown internal detail", err: "sensitive upstream failure", wantStatus: http.StatusInternalServerError, wantCode: apiError.ServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &worklistQueryService{err: errors.New(test.err)}
			controller := InferenceQueryController{InferenceQueryServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.GetProcessingRunExecutionResult(recorder, worklistQueryRequest(
				http.MethodGet, "/v1/inference/processing/runs/run-1/executions/execution-1/result", "tenant-a",
				map[string]string{"runId": "run-1", "executionId": "execution-1"},
			))

			require.Equal(t, test.wantStatus, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			response := decodeWorklistResponse(t, recorder)
			require.False(t, response.Success)
			require.Equal(t, test.wantCode, response.ErrorCode)
			require.NotContains(t, recorder.Body.String(), "sensitive upstream")
		})
	}
}

func TestWorklistQueryControllersRejectMissingAuthenticationAndInvalidPagination(t *testing.T) {
	t.Run("missing authentication", func(t *testing.T) {
		service := &worklistQueryService{}
		controller := InferenceQueryController{InferenceQueryServiceInterface: service}
		recorder := httptest.NewRecorder()

		controller.GetWorklistStudyStatuses(recorder, worklistQueryRequest(http.MethodGet, "/v1/inference/worklist/status", "", nil))

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Empty(t, service.statusInput.TenantID)
	})

	t.Run("invalid pagination", func(t *testing.T) {
		service := &worklistQueryService{}
		controller := InferenceQueryController{InferenceQueryServiceInterface: service}
		recorder := httptest.NewRecorder()

		controller.GetWorklistStudyStatuses(recorder, worklistQueryRequest(http.MethodGet, "/v1/inference/worklist/status?limit=abc", "tenant-a", nil))

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Empty(t, service.statusInput.TenantID)
	})
}

func TestWorklistQueryControllersMapServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        string
		wantStatus int
	}{
		{name: "invalid", err: apiError.InvalidPayload, wantStatus: http.StatusBadRequest},
		{name: "maximum", err: apiError.MaximumLimitReached, wantStatus: http.StatusBadRequest},
		{name: "missing", err: apiError.MissingRecord, wantStatus: http.StatusNotFound},
		{name: "forbidden", err: apiError.ForbiddenAccess, wantStatus: http.StatusForbidden},
		{name: "timeout", err: apiError.HystrixTimeout, wantStatus: http.StatusServiceUnavailable},
		{name: "database", err: apiError.DatabaseError, wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &worklistQueryService{err: errors.New(test.err)}
			controller := InferenceQueryController{InferenceQueryServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.GetProcessingRunDetail(recorder, worklistQueryRequest(http.MethodGet, "/v1/inference/processing/runs/run-1", "tenant-a", map[string]string{"runId": "run-1"}))

			require.Equal(t, test.wantStatus, recorder.Code)
			response := decodeWorklistResponse(t, recorder)
			require.False(t, response.Success)
			require.Equal(t, test.err, response.ErrorCode)
		})
	}
}
