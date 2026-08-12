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
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type manualProcessingRunService struct {
	application.InferenceCommandServiceInterface
	result serviceTypes.CreateStudyProcessingRunResult
	err    error
	input  serviceTypes.CreateStudyProcessingRun
}

func (service *manualProcessingRunService) CreateManualStudyProcessingRun(_ context.Context, data serviceTypes.CreateStudyProcessingRun) (serviceTypes.CreateStudyProcessingRunResult, error) {
	service.input = data
	return service.result, service.err
}

func processingRunRequest(studyInstanceUID string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/inference/worklist/studies/"+studyInstanceUID+"/reprocess", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("studyInstanceUID", studyInstanceUID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	return request.WithContext(ctx)
}

func TestReprocessStudyCreatesTenantScopedManualRun(t *testing.T) {
	service := &manualProcessingRunService{result: serviceTypes.CreateStudyProcessingRunResult{
		Run: entity.InferenceIngestionProcessingRun{
			ID: "run-3", RunNumber: 3,
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
		},
		Executions: []entity.InferenceIngestionProcessingJob{{ID: "execution-a"}, {ID: "execution-b"}},
		Created:    true,
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ReprocessStudy(recorder, processingRunRequest("1.2.3"))

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "tenant-a", service.input.TenantID)
	require.Equal(t, "1.2.3", service.input.StudyInstanceUID)
	require.NotNil(t, service.input.UserID)
	require.Equal(t, "user-a", *service.input.UserID)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			RunID          string `json:"runId"`
			RunNumber      int    `json:"runNumber"`
			ExpectedModels int    `json:"expectedModels"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "run-3", response.Data.RunID)
	require.Equal(t, 3, response.Data.RunNumber)
	require.Equal(t, 2, response.Data.ExpectedModels)
}

func TestReprocessStudyReturnsConflictForActiveRun(t *testing.T) {
	service := &manualProcessingRunService{err: errors.New(apiError.DuplicateRecord)}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ReprocessStudy(recorder, processingRunRequest("1.2.3"))

	require.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, apiError.DuplicateRecord, response.ErrorCode)
}

func TestReprocessStudyReturnsMachineReadableQuotaLimit(t *testing.T) {
	service := &manualProcessingRunService{err: &apiError.InferenceQuotaLimitError{
		ErrorCode: apiError.InferenceConcurrencyExceeded,
		Allowance: 50, Used: 4, Remaining: 46,
		MaxConcurrentExecutions: 2, ActiveExecutions: 2, RetryAfterSeconds: 37,
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ReprocessStudy(recorder, processingRunRequest("1.2.3"))

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "37", recorder.Header().Get("Retry-After"))
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var response struct {
		ErrorCode string `json:"errorCode"`
		Data      struct {
			Remaining        int64 `json:"remaining"`
			ActiveExecutions int64 `json:"activeExecutions"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.InferenceConcurrencyExceeded, response.ErrorCode)
	require.Equal(t, int64(46), response.Data.Remaining)
	require.Equal(t, int64(2), response.Data.ActiveExecutions)
}

func TestReprocessStudyRejectsMissingStudyUID(t *testing.T) {
	service := &manualProcessingRunService{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ReprocessStudy(recorder, processingRunRequest(""))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
