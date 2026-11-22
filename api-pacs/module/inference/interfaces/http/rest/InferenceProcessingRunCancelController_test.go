package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type cancelRunCommandService struct {
	application.InferenceCommandServiceInterface
	inputs []serviceTypes.CancelProcessingRun
	result serviceTypes.ProcessingRunDetail
	err    error
}

func (service *cancelRunCommandService) CancelProcessingRun(_ context.Context, data serviceTypes.CancelProcessingRun) (serviceTypes.ProcessingRunDetail, error) {
	service.inputs = append(service.inputs, data)
	return service.result, service.err
}

func cancelRunRequest(runID string, identity map[interface{}]interface{}) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/inference/processing/runs/"+runID+"/cancel", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("runId", runID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	for key, value := range identity {
		ctx = context.WithValue(ctx, key, value)
	}
	return request.WithContext(ctx)
}

func cancelRunIdentity() map[interface{}]interface{} {
	return map[interface{}]interface{}{iamTypes.TenantIDCtx: "tenant-a", iamTypes.UserIDCtx: "user-a"}
}

func decodeCancelRunResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func TestCancelProcessingRunReturnsTheRunDetailForTheAuthenticatedTenantAndUser(t *testing.T) {
	outcome := entity.InferenceIngestionProcessingRunOutcomePartialSuccess
	cancelledAt := time.Date(2026, time.November, 16, 10, 0, 0, 0, time.UTC)
	message := "Cancelled by user"
	service := &cancelRunCommandService{result: serviceTypes.ProcessingRunDetail{
		ProcessingRunSummary: serviceTypes.ProcessingRunSummary{
			RunID: "run-1", StudyInstanceUID: "1.2.3", RunNumber: 1,
			Trigger: entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:   entity.InferenceIngestionProcessingRunPhaseTerminal, Outcome: &outcome,
			AttentionReasons:    entity.InferenceIngestionProcessingRunAttentionReasons{},
			ProcessingRunCounts: serviceTypes.ProcessingRunCounts{Expected: 2, Completed: 1, Cancelled: 1},
		},
		Executions: []serviceTypes.ProcessingRunExecutionSummary{
			{ExecutionID: "execution-a", ModelName: "ModelA", Status: entity.InferenceIngestionProcessingJobStatusCompleted},
			{ExecutionID: "execution-b", ModelName: "ModelB", Status: entity.InferenceIngestionProcessingJobStatusCancelled, ErrorMessage: &message},
		},
		CancelledAt: &cancelledAt,
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.CancelProcessingRun(recorder, cancelRunRequest("run-1", cancelRunIdentity()))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, []serviceTypes.CancelProcessingRun{{TenantID: "tenant-a", RunID: "run-1", UserID: "user-a"}}, service.inputs)
	body := decodeCancelRunResponse(t, recorder)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]interface{})
	require.Equal(t, "run-1", data["runId"])
	require.Equal(t, "TERMINAL", data["phase"])
	require.Equal(t, "PARTIAL_SUCCESS", data["outcome"])
	require.Equal(t, float64(1), data["cancelledModels"])
	require.Equal(t, float64(1), data["completedModels"])
	require.Equal(t, false, data["cancellable"])
	require.Equal(t, "2026-11-16T10:00:00Z", data["cancelledAt"])
	require.NotContains(t, data, "waitingReason")
	executions := data["executions"].([]interface{})
	require.Equal(t, "cancelled", executions[1].(map[string]interface{})["status"])
	// The user who cancelled and the tenant are never echoed.
	require.NotContains(t, recorder.Body.String(), "user-a")
	require.NotContains(t, recorder.Body.String(), "tenant-a")
}

func TestCancelProcessingRunRequiresTenantAndUser(t *testing.T) {
	for name, identity := range map[string]map[interface{}]interface{}{
		"no identity": {},
		"no user":     {iamTypes.TenantIDCtx: "tenant-a"},
		"blank user":  {iamTypes.TenantIDCtx: "tenant-a", iamTypes.UserIDCtx: "  "},
		"no tenant":   {iamTypes.UserIDCtx: "user-a"},
	} {
		t.Run(name, func(t *testing.T) {
			service := &cancelRunCommandService{}
			controller := InferenceCommandController{InferenceCommandServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.CancelProcessingRun(recorder, cancelRunRequest("run-1", identity))

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Empty(t, service.inputs)
		})
	}
}

func TestCancelProcessingRunMapsErrors(t *testing.T) {
	for _, test := range []struct {
		serviceError string
		status       int
		errorCode    string
	}{
		// Unknown run and a run of another tenant are the same answer.
		{apiError.MissingRecord, http.StatusNotFound, apiError.MissingRecord},
		{apiError.ProcessingRunNotCancellable, http.StatusConflict, apiError.ProcessingRunNotCancellable},
		{apiError.InvalidPayload, http.StatusBadRequest, apiError.InvalidPayload},
		{apiError.HystrixTimeout, http.StatusServiceUnavailable, apiError.HystrixTimeout},
		{apiError.DatabaseError, http.StatusInternalServerError, apiError.DatabaseError},
	} {
		t.Run(test.serviceError, func(t *testing.T) {
			service := &cancelRunCommandService{err: errors.New(test.serviceError)}
			controller := InferenceCommandController{InferenceCommandServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.CancelProcessingRun(recorder, cancelRunRequest("run-9", cancelRunIdentity()))

			require.Equal(t, test.status, recorder.Code)
			body := decodeCancelRunResponse(t, recorder)
			require.Equal(t, false, body["success"])
			require.Equal(t, test.errorCode, body["errorCode"])
			require.NotContains(t, recorder.Body.String(), "runId")
		})
	}
}
