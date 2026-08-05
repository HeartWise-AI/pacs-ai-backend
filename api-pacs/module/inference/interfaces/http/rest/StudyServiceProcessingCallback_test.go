package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type callbackCaptureService struct {
	application.InferenceCommandServiceInterface
	input serviceTypes.HandleStudyServiceProcessingCallback
	calls int
}

func (service *callbackCaptureService) HandleStudyServiceProcessingCallback(
	_ context.Context,
	data serviceTypes.HandleStudyServiceProcessingCallback,
) (serviceTypes.HandleStudyServiceProcessingCallbackResult, error) {
	service.input = data
	service.calls++
	return serviceTypes.HandleStudyServiceProcessingCallbackResult{Outcome: "applied"}, nil
}

func studyServiceCallbackRequest(t *testing.T, payload string) *http.Request {
	t.Helper()
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "callback-secret")

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/inference/ingestion/candidates/candidate-123/processing",
		strings.NewReader(payload),
	)
	request.Header.Set("Authorization", "Bearer callback-secret")
	request.Header.Set("X-Request-ID", "request-123")

	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("candidate_id", "candidate-123")
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}

func TestStudyServiceProcessingCallbackMapsOrderedEventContract(t *testing.T) {
	service := &callbackCaptureService{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()
	payload := `{
		"event_id":"f97e0237-6f92-5e89-8889-17ed5cf81152",
		"sequence":3,
		"occurred_at":"2026-08-05T12:03:04Z",
		"tenant_id":"tenant-123",
		"ingestion_job_id":"ingestion-123",
		"candidate_id":"candidate-123",
		"retrieval_attempt_id":"retrieval-123",
		"study_instance_uid":"1.2.3.4",
		"processing_run_id":"run-123",
		"model_name":"PanEcho",
		"model_version":"1.0.0",
		"modality":"echocardiogram",
		"status":"skipped",
		"skip_reason":{"code":"no_usable_dicom","message":" No usable DICOM series "},
		"error_message":null,
		"study_service_job_id":"python-job-123",
		"started_at":"2026-08-05T12:00:00Z",
		"completed_at":"2026-08-05T12:03:04Z"
	}`

	controller.StudyServiceProcessingCallback(recorder, studyServiceCallbackRequest(t, payload))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, service.calls)
	require.Equal(t, "candidate-123", service.input.CandidateID)
	require.Equal(t, "request-123", service.input.RequestID)
	require.Equal(t, "f97e0237-6f92-5e89-8889-17ed5cf81152", service.input.EventID)
	require.NotNil(t, service.input.Sequence)
	require.EqualValues(t, 3, *service.input.Sequence)
	require.Equal(t, "tenant-123", service.input.TenantID)
	require.Equal(t, "ingestion-123", service.input.IngestionJobID)
	require.Equal(t, "candidate-123", service.input.PayloadCandidateID)
	require.Equal(t, "retrieval-123", service.input.RetrievalAttemptID)
	require.Equal(t, "run-123", service.input.ProcessingRunID)
	require.Equal(t, "1.2.3.4", service.input.StudyInstanceUID)
	require.Equal(t, "python-job-123", service.input.StudyServiceJobID)
	require.Equal(t, time.Date(2026, 8, 5, 12, 3, 4, 0, time.UTC), *service.input.OccurredAt)
	require.NotNil(t, service.input.SkipReason)
	require.Equal(t, "NO_USABLE_DICOM", string(service.input.SkipReason.Code))
	require.NotNil(t, service.input.SkipReason.Message)
	require.Equal(t, "No usable DICOM series", *service.input.SkipReason.Message)
}

func TestStudyServiceProcessingCallbackPreservesLegacyPayloadCompatibility(t *testing.T) {
	service := &callbackCaptureService{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()
	payload := `{
		"study_instance_uid":"1.2.3.4",
		"model_name":"PanEcho",
		"model_version":"1.0.0",
		"modality":"echocardiogram",
		"status":"running",
		"study_service_job_id":"python-job-legacy",
		"started_at":"2026-08-05T12:00:00Z"
	}`

	controller.StudyServiceProcessingCallback(recorder, studyServiceCallbackRequest(t, payload))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, service.calls)
	require.Empty(t, service.input.EventID)
	require.Nil(t, service.input.Sequence)
	require.Nil(t, service.input.OccurredAt)
	require.Nil(t, service.input.SkipReason)
}

func TestStudyServiceProcessingCallbackRejectsMalformedOrderedEventFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name: "event ID",
			payload: `{
				"event_id":"not-a-uuid",
				"sequence":1,
				"study_instance_uid":"1.2.3.4",
				"model_name":"PanEcho",
				"model_version":"1.0.0",
				"modality":"echocardiogram",
				"status":"queued",
				"study_service_job_id":"python-job-123"
			}`,
		},
		{
			name: "sequence",
			payload: `{
				"event_id":"f97e0237-6f92-5e89-8889-17ed5cf81152",
				"sequence":0,
				"study_instance_uid":"1.2.3.4",
				"model_name":"PanEcho",
				"model_version":"1.0.0",
				"modality":"echocardiogram",
				"status":"queued",
				"study_service_job_id":"python-job-123"
			}`,
		},
		{
			name: "occurred at",
			payload: `{
				"event_id":"f97e0237-6f92-5e89-8889-17ed5cf81152",
				"sequence":1,
				"occurred_at":"yesterday",
				"study_instance_uid":"1.2.3.4",
				"model_name":"PanEcho",
				"model_version":"1.0.0",
				"modality":"echocardiogram",
				"status":"queued",
				"study_service_job_id":"python-job-123"
			}`,
		},
		{
			name: "skip reason",
			payload: `{
				"event_id":"f97e0237-6f92-5e89-8889-17ed5cf81152",
				"sequence":3,
				"study_instance_uid":"1.2.3.4",
				"model_name":"PanEcho",
				"model_version":"1.0.0",
				"modality":"echocardiogram",
				"status":"skipped",
				"skip_reason":{"code":"UNKNOWN_REASON"},
				"study_service_job_id":"python-job-123"
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &callbackCaptureService{}
			controller := InferenceCommandController{InferenceCommandServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.StudyServiceProcessingCallback(
				recorder,
				studyServiceCallbackRequest(t, test.payload),
			)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Zero(t, service.calls)
		})
	}
}
