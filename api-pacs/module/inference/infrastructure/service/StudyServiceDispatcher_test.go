package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

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
