package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/orchestrator/domain/entity"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

func orchestratorTestContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, "tenant-123")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-123")
	ctx = context.WithValue(ctx, iamTypes.BearerTokenCtx, "session-token-123")
	return ctx
}

func TestCreateThreadForwardsTenantID(t *testing.T) {
	var received struct {
		Metadata    map[string]interface{} `json:"metadata"`
		BearerToken string                 `json:"bearer_token"`
		UserID      string                 `json:"user_id"`
		TenantID    string                 `json:"tenant_id"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/init_chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"conversation_id":"thread-123"}`))
	}))
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	response, err := service.CreateThread(orchestratorTestContext(), types.CreateThreadRequest{
		Metadata: map[string]interface{}{"source": "test"},
	})
	if err != nil {
		t.Fatalf("CreateThread returned error: %v", err)
	}

	if response.ThreadID != "thread-123" {
		t.Fatalf("unexpected thread id: %s", response.ThreadID)
	}
	if received.TenantID != "tenant-123" {
		t.Fatalf("unexpected tenant_id: %q", received.TenantID)
	}
	if received.UserID != "user-123" {
		t.Fatalf("unexpected user_id: %q", received.UserID)
	}
	if received.BearerToken != "session-token-123" {
		t.Fatalf("unexpected bearer_token: %q", received.BearerToken)
	}
	if received.Metadata["source"] != "test" {
		t.Fatalf("thread metadata was not forwarded: %#v", received.Metadata)
	}
}

func TestCreateMessageForwardsTenantID(t *testing.T) {
	var received struct {
		Message        string `json:"message"`
		ConversationID string `json:"conversation_id"`
		BearerToken    string `json:"bearer_token"`
		UserID         string `json:"user_id"`
		TenantID       string `json:"tenant_id"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/thread-123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"thread_id":"thread-123","status":"success","message":{"role":"assistant","content":"ok"}}`))
	}))
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}

	if received.TenantID != "tenant-123" {
		t.Fatalf("unexpected tenant_id: %q", received.TenantID)
	}
	if received.ConversationID != "thread-123" {
		t.Fatalf("unexpected conversation_id: %q", received.ConversationID)
	}
}

func TestUploadDicomPayloadForwardsTenantID(t *testing.T) {
	var received struct {
		BearerToken string `json:"bearer_token"`
		UserID      string `json:"user_id"`
		TenantID    string `json:"tenant_id"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dicom/thread-123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"conversation_id":"thread-123","status":"success","message":"ok"}`))
	}))
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	threadID := "thread-123"
	_, err := service.UploadDicomPayload(orchestratorTestContext(), types.DicomPayloadRequest{
		ThreadID: &threadID,
		Payload: []types.StudyData{
			{
				StudyInstanceUID:   "1.2.3",
				AdditionalMetadata: map[string]interface{}{},
				SeriesInstanceUIDs: []string{"4.5.6"},
			},
		},
	})
	if err != nil {
		t.Fatalf("UploadDicomPayload returned error: %v", err)
	}

	if received.TenantID != "tenant-123" {
		t.Fatalf("unexpected tenant_id: %q", received.TenantID)
	}
	if received.UserID != "user-123" {
		t.Fatalf("unexpected user_id: %q", received.UserID)
	}
	if received.BearerToken != "session-token-123" {
		t.Fatalf("unexpected bearer_token: %q", received.BearerToken)
	}
}

func newCreateMessageTestServer(t *testing.T, target interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(target); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"thread_id":"thread-123","status":"success","message":{"role":"assistant","content":"ok"}}`))
	}))
}

func TestCreateMessageForwardsViewerStudyMetadata(t *testing.T) {
	var received struct {
		Metadata map[string]interface{} `json:"metadata"`
	}
	server := newCreateMessageTestServer(t, &received)
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "What is the coronary dominance?",
		Metadata: map[string]interface{}{
			"studyInstanceUID": "2.25.4242",
			"modality":         "XA",
			"selectedSeries":   []interface{}{"2.25.4242.1"},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}

	if received.Metadata["studyInstanceUID"] != "2.25.4242" {
		t.Fatalf("studyInstanceUID was not forwarded: %#v", received.Metadata)
	}
	if received.Metadata["modality"] != "XA" {
		t.Fatalf("modality was not forwarded: %#v", received.Metadata)
	}
	series, ok := received.Metadata["selectedSeries"].([]interface{})
	if !ok || len(series) != 1 || series[0] != "2.25.4242.1" {
		t.Fatalf("selectedSeries was not forwarded: %#v", received.Metadata)
	}
}

func TestCreateMessageOmitsEmptyMetadata(t *testing.T) {
	var raw map[string]json.RawMessage
	server := newCreateMessageTestServer(t, &raw)
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	if _, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "hello",
	}); err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
	if _, present := raw["metadata"]; present {
		t.Fatalf("metadata should be omitted when empty: %s", raw["metadata"])
	}
}
