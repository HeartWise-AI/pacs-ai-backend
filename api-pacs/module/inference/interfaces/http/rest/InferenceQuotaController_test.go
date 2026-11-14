package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type inferenceQuotaQueryService struct {
	application.InferenceQueryServiceInterface
	status   serviceTypes.InferenceQuotaStatus
	err      error
	tenantID string
	userID   string
}

func (service *inferenceQuotaQueryService) GetInferenceQuota(_ context.Context, tenantID, userID string) (serviceTypes.InferenceQuotaStatus, error) {
	service.tenantID = tenantID
	service.userID = userID
	return service.status, service.err
}

func inferenceQuotaRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/inference/quota", nil)
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	return request.WithContext(ctx)
}

func TestGetInferenceQuotaReturnsAuthenticatedScopeAndResetMetadata(t *testing.T) {
	service := &inferenceQuotaQueryService{status: serviceTypes.InferenceQuotaStatus{
		Allowance: 50, Used: 7, Remaining: 43, Window: 24 * time.Hour,
		ResetAfter: 10 * time.Minute, MaxConcurrentExecutions: 2,
		ActiveExecutions: 1, ConcurrentRetryAfter: 45 * time.Second,
		MaxQueuedExecutions: 50, QueuedExecutions: 6, QueuedRetryAfter: 90 * time.Second,
	}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetInferenceQuota(recorder, inferenceQuotaRequest())

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "tenant-a", service.tenantID)
	require.Equal(t, "user-a", service.userID)
	var response struct {
		Data struct {
			Allowance                    int64   `json:"allowance"`
			Remaining                    int64   `json:"remaining"`
			ResetAt                      *uint64 `json:"resetAt"`
			MaxConcurrentExecutions      int64   `json:"maxConcurrentExecutions"`
			ConcurrencyRetryAfterSeconds int64   `json:"concurrencyRetryAfterSeconds"`
			ActiveExecutions             int64   `json:"activeExecutions"`
			MaxQueuedExecutions          int64   `json:"maxQueuedExecutions"`
			QueuedExecutions             int64   `json:"queuedExecutions"`
			QueuedRetryAfterSeconds      int64   `json:"queuedRetryAfterSeconds"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, int64(50), response.Data.Allowance)
	require.Equal(t, int64(43), response.Data.Remaining)
	require.NotNil(t, response.Data.ResetAt)
	require.Equal(t, int64(2), response.Data.MaxConcurrentExecutions)
	require.Equal(t, int64(45), response.Data.ConcurrencyRetryAfterSeconds)
	require.Equal(t, int64(1), response.Data.ActiveExecutions)
	require.Equal(t, int64(50), response.Data.MaxQueuedExecutions)
	require.Equal(t, int64(6), response.Data.QueuedExecutions)
	require.Equal(t, int64(90), response.Data.QueuedRetryAfterSeconds)
}

func TestWriteInferenceQuotaErrorRendersQueueLimit(t *testing.T) {
	recorder := httptest.NewRecorder()
	handled := writeInferenceQuotaError(recorder, &apiError.InferenceQuotaLimitError{
		ErrorCode: apiError.InferenceQueueLimitExceeded, Allowance: 500, Used: 50, Remaining: 450,
		MaxConcurrentExecutions: 2, MaxQueuedExecutions: 50, QueuedExecutions: 50, RetryAfterSeconds: 30,
	})

	require.True(t, handled)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "30", recorder.Header().Get("Retry-After"))
	var response struct {
		ErrorCode string `json:"errorCode"`
		Data      struct {
			MaxQueuedExecutions int64 `json:"maxQueuedExecutions"`
			QueuedExecutions    int64 `json:"queuedExecutions"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.InferenceQueueLimitExceeded, response.ErrorCode)
	require.Equal(t, int64(50), response.Data.MaxQueuedExecutions)
	require.Equal(t, int64(50), response.Data.QueuedExecutions)
}

func TestGetInferenceQuotaFailsClosedWhenRedisIsUnavailable(t *testing.T) {
	service := &inferenceQuotaQueryService{err: errors.New(apiError.InferenceQuotaUnavailable)}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetInferenceQuota(recorder, inferenceQuotaRequest())

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.InferenceQuotaUnavailable, response.ErrorCode)
}
