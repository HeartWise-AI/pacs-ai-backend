package rest

import (
	"bytes"
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
	types "api-pacs/module/inference/interfaces/http"
)

type manualProcessingRunService struct {
	application.InferenceCommandServiceInterface
	result serviceTypes.CreateStudyProcessingRunResult
	err    error
	input  serviceTypes.CreateStudyProcessingRun
	inputs []serviceTypes.CreateStudyProcessingRun
	run    func(serviceTypes.CreateStudyProcessingRun) (serviceTypes.CreateStudyProcessingRunResult, error)
}

func (service *manualProcessingRunService) CreateManualStudyProcessingRun(_ context.Context, data serviceTypes.CreateStudyProcessingRun) (serviceTypes.CreateStudyProcessingRunResult, error) {
	service.input = data
	service.inputs = append(service.inputs, data)
	if service.run != nil {
		return service.run(data)
	}
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

func bulkProcessingRunRequest(t *testing.T, payload interface{}) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/inference/worklist/studies/reprocess-batch", bytes.NewReader(body))
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	return request.WithContext(ctx)
}

func TestReprocessStudiesReturnsOrderedPerStudyResults(t *testing.T) {
	service := &manualProcessingRunService{run: func(data serviceTypes.CreateStudyProcessingRun) (serviceTypes.CreateStudyProcessingRunResult, error) {
		if data.StudyInstanceUID == "1.2.4" {
			return serviceTypes.CreateStudyProcessingRunResult{}, errors.New(apiError.DuplicateRecord)
		}
		return serviceTypes.CreateStudyProcessingRunResult{
			Run: entity.InferenceIngestionProcessingRun{
				ID: "run-1", RunNumber: 2,
				RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
				Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
			},
			Executions: []entity.InferenceIngestionProcessingJob{{ID: "execution-1"}},
		}, nil
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ReprocessStudies(recorder, bulkProcessingRunRequest(t, map[string]interface{}{
		"studyInstanceUIDs": []string{" 1.2.3 ", "1.2.4"},
	}))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Len(t, service.inputs, 2)
	require.Equal(t, "1.2.3", service.inputs[0].StudyInstanceUID)
	require.Equal(t, "tenant-a", service.inputs[0].TenantID)
	require.Equal(t, "user-a", *service.inputs[0].UserID)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			AcceptedCount int `json:"acceptedCount"`
			RejectedCount int `json:"rejectedCount"`
			Results       []struct {
				StudyInstanceUID string                                  `json:"studyInstanceUID"`
				Accepted         bool                                    `json:"accepted"`
				Run              *types.CreateStudyProcessingRunResponse `json:"run"`
				ErrorCode        string                                  `json:"errorCode"`
			} `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, 1, response.Data.AcceptedCount)
	require.Equal(t, 1, response.Data.RejectedCount)
	require.Equal(t, "1.2.3", response.Data.Results[0].StudyInstanceUID)
	require.True(t, response.Data.Results[0].Accepted)
	require.Equal(t, "run-1", response.Data.Results[0].Run.RunID)
	require.Equal(t, "1.2.4", response.Data.Results[1].StudyInstanceUID)
	require.False(t, response.Data.Results[1].Accepted)
	require.Equal(t, apiError.DuplicateRecord, response.Data.Results[1].ErrorCode)
}

func TestReprocessStudiesReturnsQuotaLimitPerStudy(t *testing.T) {
	service := &manualProcessingRunService{run: func(serviceTypes.CreateStudyProcessingRun) (serviceTypes.CreateStudyProcessingRunResult, error) {
		return serviceTypes.CreateStudyProcessingRunResult{}, &apiError.InferenceQuotaLimitError{
			ErrorCode: apiError.InferenceConcurrencyExceeded, RetryAfterSeconds: 37,
		}
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ReprocessStudies(recorder, bulkProcessingRunRequest(t, map[string]interface{}{
		"studyInstanceUIDs": []string{"1.2.3"},
	}))

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Results []types.BulkReprocessStudyResultResponse `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.InferenceConcurrencyExceeded, response.Data.Results[0].ErrorCode)
	require.Equal(t, int64(37), response.Data.Results[0].RetryAfterSeconds)
}

func TestReprocessStudiesRejectsDuplicateOrOversizedSelections(t *testing.T) {
	controller := InferenceCommandController{InferenceCommandServiceInterface: &manualProcessingRunService{}}

	for name, studyInstanceUIDs := range map[string][]string{
		"duplicate": {"1.2.3", "1.2.3"},
		"empty":     {},
		"too many":  {"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			controller.ReprocessStudies(recorder, bulkProcessingRunRequest(t, map[string]interface{}{
				"studyInstanceUIDs": studyInstanceUIDs,
			}))

			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
