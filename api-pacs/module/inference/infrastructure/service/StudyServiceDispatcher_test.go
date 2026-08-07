package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func TestStudyServiceDispatcherDispatchStudyDecodesAcceptedResponses(t *testing.T) {
	testCases := []struct {
		name           string
		statusCode     int
		alreadyPresent bool
	}{
		{name: "new job", statusCode: http.StatusAccepted, alreadyPresent: false},
		{name: "idempotent duplicate", statusCode: http.StatusOK, alreadyPresent: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			processingRunID := "run-123"
			var (
				request      serviceTypes.DispatchStudyRequest
				requestErr   error
				responseErr  error
				gotMethod    string
				gotPath      string
				gotAuth      string
				gotRequestID string
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				gotRequestID = r.Header.Get("X-Request-ID")
				requestErr = json.NewDecoder(r.Body).Decode(&request)
				w.WriteHeader(testCase.statusCode)
				responseErr = json.NewEncoder(w).Encode(map[string]any{
					"job_id":          "study-job-123",
					"already_present": testCase.alreadyPresent,
				})
			}))
			defer server.Close()

			dispatcher := &StudyServiceDispatcher{
				StudyServiceBaseURL:     server.URL,
				StudyServiceIngestToken: "ingest-token",
				StudyServiceClient:      server.Client(),
			}

			response, err := dispatcher.DispatchStudy(context.Background(), serviceTypes.DispatchStudyRequest{
				XRequestID:       "request-123",
				ProcessingRunID:  &processingRunID,
				StudyInstanceUID: "study-123",
				OrthancStudyID:   "orthanc-study-123",
				Modality:         "US",
				ModelName:        "model-one",
			})

			if err != nil {
				t.Fatalf("DispatchStudy returned error: %v", err)
			}
			if requestErr != nil {
				t.Fatalf("decode request: %v", requestErr)
			}
			if responseErr != nil {
				t.Fatalf("encode response: %v", responseErr)
			}
			if gotMethod != http.MethodPost || gotPath != "/ingest/study" {
				t.Fatalf("unexpected request: %s %s", gotMethod, gotPath)
			}
			if gotAuth != "Bearer ingest-token" || gotRequestID != "request-123" {
				t.Fatalf("unexpected headers: authorization=%q request_id=%q", gotAuth, gotRequestID)
			}
			if request.ProcessingRunID == nil || *request.ProcessingRunID != processingRunID {
				t.Fatalf("unexpected processing run ID: %#v", request.ProcessingRunID)
			}
			if response.JobID != "study-job-123" || response.AlreadyPresent != testCase.alreadyPresent {
				t.Fatalf("unexpected dispatch response: %#v", response)
			}
			if response.StatusCode != testCase.statusCode {
				t.Fatalf("unexpected status code: %d", response.StatusCode)
			}
		})
	}
}

func TestStudyServiceDispatcherDispatchStudyReturnsTypedRetryableError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceClient: server.Client()}
	_, err := dispatcher.DispatchStudy(context.Background(), serviceTypes.DispatchStudyRequest{})

	var httpErr *DispatchStudyHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected DispatchStudyHTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status code: %d", httpErr.StatusCode)
	}
	if httpErr.RetryAfter != 7*time.Second {
		t.Fatalf("unexpected retry delay: %s", httpErr.RetryAfter)
	}
	if !shouldRetryStudyServiceDispatchHTTPError(*httpErr) {
		t.Fatal("expected service-unavailable response to be retryable")
	}
}

func TestStudyServiceDispatcherDispatchStudyReturnsTypedPermanentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid model", http.StatusBadRequest)
	}))
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceClient: server.Client()}
	_, err := dispatcher.DispatchStudy(context.Background(), serviceTypes.DispatchStudyRequest{})

	var httpErr *DispatchStudyHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected DispatchStudyHTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", httpErr.StatusCode)
	}
	if shouldRetryStudyServiceDispatchHTTPError(*httpErr) {
		t.Fatal("expected bad-request response to be permanent")
	}
}

func TestBuildDispatchStudyRequestIncludesProcessingRunID(t *testing.T) {
	orthancStudyID := "orthanc-study-1"
	processingRunID := " run-123 "
	dispatcher := &StudyServiceDispatcher{}

	request, err := dispatcher.BuildDispatchStudyRequest(context.Background(), serviceTypes.BuildStudyServiceDispatchRequestInput{
		IngestionJob: entity.InferenceIngestionJob{
			ID: "ingestion-1", TenantID: "tenant-a", ModelName: "model-one", ModelVersion: "1.0", Modalities: []string{"US"},
		},
		Candidate: entity.InferenceIngestionCandidate{
			ID: "candidate-1", TenantID: "tenant-a", IngestionJobID: "ingestion-1", StudyInstanceUID: "study-1",
		},
		OrthancStudyID:  &orthancStudyID,
		ProcessingRunID: &processingRunID,
	})

	if err != nil {
		t.Fatalf("BuildDispatchStudyRequest returned error: %v", err)
	}
	if request.ProcessingRunID == nil || *request.ProcessingRunID != "run-123" {
		t.Fatalf("unexpected processing run id: %#v", request.ProcessingRunID)
	}
	if request.Modality != "echocardiogram" {
		t.Fatalf("unexpected canonical modality: %q", request.Modality)
	}
}

func TestResolveDispatchModalityReturnsCanonicalStudyServiceValue(t *testing.T) {
	modalitiesInStudy := "XA\\SR"
	modality, err := resolveDispatchModality(
		entity.InferenceIngestionJob{Modalities: []string{"XA"}},
		entity.InferenceIngestionCandidate{ModalitiesInStudy: &modalitiesInStudy},
	)

	if err != nil {
		t.Fatalf("resolveDispatchModality returned error: %v", err)
	}
	if modality != "angiogram" {
		t.Fatalf("unexpected canonical modality: %q", modality)
	}
}

func TestStudyServiceDispatcherGetJobsByCandidate(t *testing.T) {
	t.Helper()

	var (
		gotAuthorization string
		gotTenantID      string
		gotRequestID     string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotTenantID = r.Header.Get("X-Tenant-ID")
		gotRequestID = r.Header.Get("X-Request-ID")

		if r.URL.Path != "/jobs/by-candidate/candidate-123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		if err := json.NewEncoder(w).Encode(map[string]any{
			"jobs": []map[string]any{
				{
					"job_id":             "job-123",
					"study_instance_uid": "1.2.3.4",
					"tenant_id":          "tenant-123",
					"candidate_id":       "candidate-123",
					"processing_run_id":  "run-123",
					"modality":           "echocardiogram",
					"model_name":         "PanEcho",
					"model_version":      "1.0.0",
					"status":             "completed",
				},
			},
			"page":      1,
			"page_size": 250,
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{
		StudyServiceBaseURL:       server.URL,
		StudyServiceOperatorToken: "operator-token",
		StudyServiceClient:        server.Client(),
	}

	jobs, err := dispatcher.GetJobsByCandidate(context.Background(), "tenant-123", "candidate-123")
	if err != nil {
		t.Fatalf("GetJobsByCandidate returned error: %v", err)
	}

	if gotAuthorization != "Bearer operator-token" {
		t.Fatalf("unexpected authorization header: %q", gotAuthorization)
	}
	if gotTenantID != "tenant-123" {
		t.Fatalf("unexpected tenant header: %q", gotTenantID)
	}
	if gotRequestID == "" {
		t.Fatalf("expected X-Request-ID header to be set")
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].JobID != "job-123" {
		t.Fatalf("unexpected job id: %q", jobs[0].JobID)
	}
	if jobs[0].Status != "completed" {
		t.Fatalf("unexpected job status: %q", jobs[0].Status)
	}
	if jobs[0].ProcessingRunID == nil || *jobs[0].ProcessingRunID != "run-123" {
		t.Fatalf("unexpected processing run id: %#v", jobs[0].ProcessingRunID)
	}
}

func TestStudyServiceDispatcherGetJobByID(t *testing.T) {
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		if r.URL.Path != "/jobs/python-job-123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"job_id":             "python-job-123",
			"study_instance_uid": "1.2.3",
			"tenant_id":          "tenant-a",
			"candidate_id":       "candidate-1",
			"processing_run_id":  "run-1",
			"model_name":         "EchoPrime",
			"status":             "running",
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{
		StudyServiceBaseURL:       server.URL,
		StudyServiceOperatorToken: "operator-token",
		StudyServiceClient:        server.Client(),
	}
	job, found, err := dispatcher.GetJobByID(context.Background(), "tenant-a", "python-job-123")

	if err != nil {
		t.Fatalf("GetJobByID returned error: %v", err)
	}
	if !found {
		t.Fatal("expected job to be found")
	}
	if job.JobID != "python-job-123" || job.ProcessingRunID == nil || *job.ProcessingRunID != "run-1" {
		t.Fatalf("unexpected job: %#v", job)
	}
	if gotHeaders.Get("Authorization") != "Bearer operator-token" {
		t.Fatalf("unexpected authorization header: %q", gotHeaders.Get("Authorization"))
	}
	if gotHeaders.Get("X-Tenant-ID") != "tenant-a" {
		t.Fatalf("unexpected tenant header: %q", gotHeaders.Get("X-Tenant-ID"))
	}
	if gotHeaders.Get("X-Request-ID") == "" {
		t.Fatal("expected X-Request-ID header")
	}
}

func TestStudyServiceDispatcherGetJobByIDTreatsNotFoundAsExpected(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{
		StudyServiceBaseURL: server.URL,
		StudyServiceClient:  server.Client(),
	}
	job, found, err := dispatcher.GetJobByID(context.Background(), "tenant-a", "missing-job")

	if err != nil {
		t.Fatalf("GetJobByID returned error: %v", err)
	}
	if found {
		t.Fatalf("expected missing job, got %#v", job)
	}
}

func TestStudyServiceDispatcherGetJobsByProcessingRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/by-processing-run/run-123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Tenant-ID") != "tenant-a" {
			t.Fatalf("unexpected tenant: %q", r.Header.Get("X-Tenant-ID"))
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"jobs": []map[string]any{{
				"job_id": "job-1", "processing_run_id": "run-123", "candidate_id": "candidate-1",
				"model_name": "EchoPrime", "status": "running",
			}},
			"page": 1, "page_size": 250,
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceClient: server.Client()}
	jobs, err := dispatcher.GetJobsByProcessingRun(context.Background(), "tenant-a", "run-123")

	if err != nil {
		t.Fatalf("GetJobsByProcessingRun returned error: %v", err)
	}
	if len(jobs) != 1 || jobs[0].JobID != "job-1" {
		t.Fatalf("unexpected jobs: %#v", jobs)
	}
}

func TestStudyServiceDispatcherGetJobsByProcessingRunTreatsNotFoundAsFallback(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceClient: server.Client()}

	jobs, err := dispatcher.GetJobsByProcessingRun(context.Background(), "tenant-a", "run-123")

	if err != nil {
		t.Fatalf("GetJobsByProcessingRun returned error: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected empty fallback result, got %#v", jobs)
	}
}

func TestStudyServiceDispatcherGetCallbackDeadLetters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/callbacks/dead-letters" || r.URL.Query().Get("limit") != "250" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if r.Header.Get("X-Tenant-ID") != "tenant-a" {
			t.Fatalf("unexpected tenant: %q", r.Header.Get("X-Tenant-ID"))
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"dead_letters": []map[string]any{{
				"dead_letter_id": "dead-letter-1",
				"job_id":         "job-1",
				"candidate_id":   "candidate-1",
				"job_status":     "running",
				"payload_json": map[string]any{
					"candidate_id": "candidate-1", "processing_run_id": "run-1", "model_name": "EchoPrime",
				},
				"attempts": 3, "last_error": "connection refused",
			}},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceClient: server.Client()}
	deadLetters, err := dispatcher.GetCallbackDeadLetters(context.Background(), "tenant-a")

	if err != nil {
		t.Fatalf("GetCallbackDeadLetters returned error: %v", err)
	}
	if len(deadLetters) != 1 || deadLetters[0].Payload.ProcessingRunID != "run-1" {
		t.Fatalf("unexpected dead letters: %#v", deadLetters)
	}
}

func TestStudyServiceDispatcherGetCallbackDeadLettersTreatsNotFoundAsFallback(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	dispatcher := &StudyServiceDispatcher{StudyServiceBaseURL: server.URL, StudyServiceClient: server.Client()}

	deadLetters, err := dispatcher.GetCallbackDeadLetters(context.Background(), "tenant-a")

	if err != nil {
		t.Fatalf("GetCallbackDeadLetters returned error: %v", err)
	}
	if len(deadLetters) != 0 {
		t.Fatalf("expected empty fallback result, got %#v", deadLetters)
	}
}
