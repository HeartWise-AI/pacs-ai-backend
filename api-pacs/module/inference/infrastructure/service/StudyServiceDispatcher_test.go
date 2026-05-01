package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		StudyServiceBaseURL:      server.URL,
		StudyServiceOperatorToken: "operator-token",
		StudyServiceClient:       server.Client(),
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
}
