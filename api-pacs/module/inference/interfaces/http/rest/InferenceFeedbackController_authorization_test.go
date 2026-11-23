package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type recordingFeedbackCommandService struct {
	application.InferenceCommandServiceInterface
	calls []serviceTypes.UpdateModelFeedback
}

func (service *recordingFeedbackCommandService) UpdateModelFeedback(_ context.Context, data serviceTypes.UpdateModelFeedback) error {
	service.calls = append(service.calls, data)
	return nil
}

type listFeedbackQueryService struct {
	application.InferenceQueryServiceInterface
	err   error
	calls []serviceTypes.ListModelFeedback
}

func (service *listFeedbackQueryService) ListModelFeedback(_ context.Context, data serviceTypes.ListModelFeedback) (serviceTypes.ModelFeedbackList, error) {
	service.calls = append(service.calls, data)
	if service.err != nil {
		return serviceTypes.ModelFeedbackList{}, service.err
	}
	return serviceTypes.ModelFeedbackList{Summary: []serviceTypes.ModelFeedbackSummary{}, Items: []serviceTypes.ModelFeedbackListItem{}}, nil
}

func feedbackRequestWithIdentity(request *http.Request, role string) *http.Request {
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	ctx = context.WithValue(ctx, iamTypes.RoleCtx, role)
	return request.WithContext(ctx)
}

type forbiddenFeedbackCommandService struct {
	application.InferenceCommandServiceInterface
}

func (*forbiddenFeedbackCommandService) UpdateModelFeedback(context.Context, serviceTypes.UpdateModelFeedback) error {
	return errors.New(apiError.ForbiddenAccess)
}

func TestUpdateModelFeedbackReturnsForbiddenForUnownedFeedback(t *testing.T) {
	controller := &InferenceCommandController{
		InferenceCommandServiceInterface: &forbiddenFeedbackCommandService{},
	}
	body := bytes.NewBufferString(`{
		"id":"feedback-other-user",
		"inferenceModelId":"inference-model-a",
		"modelId":"model-a",
		"feedbackType":"APPROVE"
	}`)
	request := httptest.NewRequest(http.MethodPut, "/feedback/update", body)
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()

	controller.UpdateModelFeedback(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestUpdateModelFeedbackForwardsTrimmedReportReference(t *testing.T) {
	service := &recordingFeedbackCommandService{}
	controller := &InferenceCommandController{InferenceCommandServiceInterface: service}
	body := bytes.NewBufferString(`{
		"inferenceModelId":"inference-model-a",
		"modelId":"inference-model-a-1.0.0",
		"feedbackType":"APPROVE",
		"runId":" run-1 ",
		"executionId":"execution-1",
		"studyServiceJobId":"job-1"
	}`)
	request := feedbackRequestWithIdentity(httptest.NewRequest(http.MethodPut, "/feedback/update", body), "USER")
	recorder := httptest.NewRecorder()

	controller.UpdateModelFeedback(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, service.calls, 1)
	require.Equal(t, "run-1", service.calls[0].RunID)
	require.Equal(t, "execution-1", service.calls[0].ExecutionID)
	require.Equal(t, "job-1", service.calls[0].StudyServiceJobID)
}

func TestUpdateModelFeedbackRejectsOverlongRunID(t *testing.T) {
	service := &recordingFeedbackCommandService{}
	controller := &InferenceCommandController{InferenceCommandServiceInterface: service}
	body := bytes.NewBufferString(`{
		"inferenceModelId":"inference-model-a",
		"modelId":"inference-model-a-1.0.0",
		"feedbackType":"APPROVE",
		"runId":"` + strings.Repeat("r", 129) + `"
	}`)
	request := feedbackRequestWithIdentity(httptest.NewRequest(http.MethodPut, "/feedback/update", body), "USER")
	recorder := httptest.NewRecorder()

	controller.UpdateModelFeedback(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, service.calls)
}

func TestListModelFeedbackMapsForbiddenAccess(t *testing.T) {
	controller := &InferenceQueryController{
		InferenceQueryServiceInterface: &listFeedbackQueryService{err: errors.New(apiError.ForbiddenAccess)},
	}
	request := feedbackRequestWithIdentity(httptest.NewRequest(http.MethodGet, "/model/feedback/list", nil), "USER")
	recorder := httptest.NewRecorder()

	controller.ListModelFeedback(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestListModelFeedbackRejectsInvalidDate(t *testing.T) {
	service := &listFeedbackQueryService{}
	controller := &InferenceQueryController{InferenceQueryServiceInterface: service}
	request := feedbackRequestWithIdentity(httptest.NewRequest(http.MethodGet, "/model/feedback/list?from=yesterday", nil), "ADMIN")
	recorder := httptest.NewRecorder()

	controller.ListModelFeedback(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, service.calls)
}

func TestListModelFeedbackMapsServiceErrors(t *testing.T) {
	for code, want := range map[string]int{
		apiError.InvalidPayload: http.StatusBadRequest,
		apiError.FirestoreError: http.StatusInternalServerError,
		apiError.HystrixTimeout: http.StatusServiceUnavailable,
	} {
		controller := &InferenceQueryController{
			InferenceQueryServiceInterface: &listFeedbackQueryService{err: errors.New(code)},
		}
		request := feedbackRequestWithIdentity(httptest.NewRequest(http.MethodGet, "/model/feedback/list", nil), "ADMIN")
		recorder := httptest.NewRecorder()

		controller.ListModelFeedback(recorder, request)

		require.Equal(t, want, recorder.Code, code)
	}
}

func TestListModelFeedbackUsesContextTenantAndRole(t *testing.T) {
	service := &listFeedbackQueryService{}
	controller := &InferenceQueryController{InferenceQueryServiceInterface: service}
	request := feedbackRequestWithIdentity(httptest.NewRequest(
		http.MethodGet,
		"/model/feedback/list?tenantId=tenant-b&role=OWNER&model=deepcoro&feedbackType=REJECT&from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00%2B01:00",
		nil,
	), "ADMIN")
	recorder := httptest.NewRecorder()

	controller.ListModelFeedback(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Len(t, service.calls, 1)
	call := service.calls[0]
	require.Equal(t, "tenant-a", call.TenantID)
	require.Equal(t, "ADMIN", call.Role)
	require.Equal(t, "deepcoro", call.Model)
	require.Equal(t, "REJECT", call.FeedbackType)
	require.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), *call.From)
	require.Equal(t, time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC), *call.To)
	require.JSONEq(t, `{"summary":[],"items":[],"truncated":false}`, string(mustDataJSON(t, recorder.Body.Bytes())))
}

func TestListModelFeedbackRequiresTenant(t *testing.T) {
	service := &listFeedbackQueryService{}
	controller := &InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.ListModelFeedback(recorder, httptest.NewRequest(http.MethodGet, "/model/feedback/list", nil))

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Empty(t, service.calls)
}

func mustDataJSON(t *testing.T, body []byte) []byte {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Data
}
