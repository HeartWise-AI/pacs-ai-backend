package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestCreateMessagePassesChatActivityThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"conversation_id":"thread-123","status":"success","message":{"role":"assistant","content":"ok"},
			"activity":{"status":"done","started_at":"2026-11-14T22:27:20.000Z","ended_at":"2026-11-14T22:28:18.000Z","elapsed_ms":58000,
			"steps":[
				{"id":1,"key":"delegate_angiogram","kind":"delegation","label":"Consulting the angiography specialist","model":null,"status":"done","started_at":"2026-11-14T22:27:23.000Z","ended_at":"2026-11-14T22:28:10.000Z","duration_ms":47000},
				{"id":2,"key":"run_model","kind":"tool","label":"Running DeepCORO-SCAD","model":"DeepCORO-SCAD","status":"failed","started_at":"2026-11-14T22:27:29.000Z","ended_at":"2026-11-14T22:27:59.000Z","duration_ms":30000}
			]}}`))
	}))
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	response, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "Summarize all findings",
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
	if response.Activity == nil {
		t.Fatal("activity was not passed through")
	}
	if response.Activity.Status != "done" || response.Activity.ElapsedMs != 58000 {
		t.Fatalf("unexpected activity: %#v", response.Activity)
	}
	if len(response.Activity.Steps) != 2 {
		t.Fatalf("unexpected steps: %#v", response.Activity.Steps)
	}
	failed := response.Activity.Steps[1]
	if failed.Key != "run_model" || failed.Status != "failed" || failed.Model == nil || *failed.Model != "DeepCORO-SCAD" {
		t.Fatalf("unexpected step: %#v", failed)
	}
	if failed.DurationMs == nil || *failed.DurationMs != 30000 {
		t.Fatalf("unexpected duration: %#v", failed.DurationMs)
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, present := body["activity"]; !present {
		t.Fatalf("activity missing from the response body: %s", encoded)
	}
}

func TestCreateMessageOmitsActivityFromAnOlderChatBackend(t *testing.T) {
	var received map[string]json.RawMessage
	server := newCreateMessageTestServer(t, &received)
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	response, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
	if response.Activity != nil {
		t.Fatalf("unexpected activity: %#v", response.Activity)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), `"activity"`) {
		t.Fatalf("activity should be omitted when the chat backend sends none: %s", encoded)
	}
}

func TestCreateMessagePassesChatCitationsThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"conversation_id":"thread-123","status":"success","message":{"role":"assistant","content":"LAD 80% [1]\nGuideline [2]"},
			"citations":[
				{"index":1,"kind":"model_result","modelName":"DeepCORO-JEPA","modelVersion":"2.0.0","completedAt":"2026-11-13 09:00:00",
				 "studyInstanceUID":"2.25.900000000000000000000000000000000000001","studyServiceJobId":"job-1","title":"DeepCORO-JEPA",
				 "summary":"Coronary reading: 2 significant stenosis segment(s)","outputMode":null,"a_future_field":{"kept":false}},
				{"index":2,"kind":"web_source","title":"2024 ESC Guidelines","url":"https://example.org/guideline"},
				{"index":3,"kind":"a_future_kind"}
			]}`))
	}))
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	response, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "Summarize all findings",
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
	if len(response.Citations) != 3 {
		t.Fatalf("unexpected citations: %#v", response.Citations)
	}
	model := response.Citations[0]
	if model.Index != 1 || model.Kind != "model_result" || model.ModelName != "DeepCORO-JEPA" || model.ModelVersion != "2.0.0" ||
		model.CompletedAt != "2026-11-13 09:00:00" || model.StudyInstanceUID != "2.25.900000000000000000000000000000000000001" ||
		model.StudyServiceJobID != "job-1" || model.Title != "DeepCORO-JEPA" ||
		model.Summary != "Coronary reading: 2 significant stenosis segment(s)" || model.OutputMode != nil {
		t.Fatalf("unexpected model result citation: %#v", model)
	}
	source := response.Citations[1]
	if source.Index != 2 || source.Kind != "web_source" || source.Title != "2024 ESC Guidelines" || source.URL != "https://example.org/guideline" {
		t.Fatalf("unexpected web source citation: %#v", source)
	}
	if unknown := response.Citations[2]; unknown.Index != 3 || unknown.Kind != "a_future_kind" {
		t.Fatalf("unexpected citation of an unknown kind: %#v", unknown)
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var body struct {
		Citations []map[string]interface{} `json:"citations"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Citations) != 3 {
		t.Fatalf("citations missing from the response body: %s", encoded)
	}
	if body.Citations[0]["studyServiceJobId"] != "job-1" || body.Citations[0]["index"] != float64(1) {
		t.Fatalf("unexpected model result citation in the body: %s", encoded)
	}
	// A web source carries only its own fields: no empty model fields.
	if len(body.Citations[1]) != 4 || body.Citations[1]["url"] != "https://example.org/guideline" {
		t.Fatalf("unexpected web source citation in the body: %s", encoded)
	}
}

func TestCreateMessageOmitsCitationsWhenTheChatBackendSendsNone(t *testing.T) {
	var received map[string]json.RawMessage
	server := newCreateMessageTestServer(t, &received)
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	response, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{
		ThreadID: "thread-123",
		Content:  "hello",
	})
	if err != nil {
		t.Fatalf("CreateMessage returned error: %v", err)
	}
	if response.Citations != nil {
		t.Fatalf("unexpected citations: %#v", response.Citations)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), `"citations"`) {
		t.Fatalf("citations should be omitted when the chat backend sends none: %s", encoded)
	}
}

func TestGetChatProgressForwardsIdentityAndReturnsSteps(t *testing.T) {
	var path, rawQuery, authorization string
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		path, rawQuery, query, authorization = r.URL.Path, r.URL.RawQuery, r.URL.Query(), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"conversation_id":"thread-123","active":true,"status":"running","started_at":"2026-11-14T22:27:20.000Z","ended_at":null,"elapsed_ms":9120,
			"steps":[{"id":1,"key":"model_result","kind":"tool","label":"Reading the stored DeepCORO-JEPA result","model":"DeepCORO-JEPA","status":"running","started_at":"2026-11-14T22:27:29.000Z","ended_at":null,"duration_ms":null}]}`))
	}))
	defer server.Close()

	service := &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}

	progress, err := service.GetChatProgress(orchestratorTestContext(), "thread-123")
	if err != nil {
		t.Fatalf("GetChatProgress returned error: %v", err)
	}

	if path != "/chat/thread-123/progress" {
		t.Fatalf("unexpected path: %s", path)
	}
	if query.Get("user_id") != "user-123" || query.Get("tenant_id") != "tenant-123" {
		t.Fatalf("user and tenant were not forwarded: %s", rawQuery)
	}
	if authorization != "Bearer session-token-123" {
		t.Fatalf("unexpected Authorization header: %q", authorization)
	}
	if strings.Contains(rawQuery, "session-token-123") {
		t.Fatalf("the session token must not travel in the URL: %s", rawQuery)
	}
	if !progress.Active || progress.Status != "running" || progress.ThreadID != "thread-123" || progress.ElapsedMs != 9120 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	if len(progress.Steps) != 1 || progress.Steps[0].Label != "Reading the stored DeepCORO-JEPA result" {
		t.Fatalf("unexpected steps: %#v", progress.Steps)
	}
	if progress.Steps[0].EndedAt != nil || progress.Steps[0].DurationMs != nil {
		t.Fatalf("a running step has no end: %#v", progress.Steps[0])
	}

	encoded, _ := json.Marshal(progress)
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("unmarshal progress: %v", err)
	}
	for _, field := range []string{"thread_id", "active", "status", "elapsed_ms", "steps"} {
		if _, present := body[field]; !present {
			t.Fatalf("%s missing from %s", field, encoded)
		}
	}
}

func TestGetChatProgressWithNothingInFlightReturnsAnEmptyStepList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"conversation_id":"thread-123","active":false,"status":"idle","elapsed_ms":0,"steps":[]}`))
	}))
	defer server.Close()

	service := &OrchestratorService{OrchestratorAPIURL: server.URL, OrchestratorClient: server.Client()}

	progress, err := service.GetChatProgress(orchestratorTestContext(), "thread-123")
	if err != nil {
		t.Fatalf("GetChatProgress returned error: %v", err)
	}
	if progress.Active || progress.Status != "idle" || progress.Steps == nil || len(progress.Steps) != 0 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
	encoded, _ := json.Marshal(progress)
	if !strings.Contains(string(encoded), `"steps":[]`) {
		t.Fatalf("steps must serialize as an empty list: %s", encoded)
	}
}

func TestGetChatProgressReportsAnOlderChatBackendAsUnsupported(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
		}))
		service := &OrchestratorService{OrchestratorAPIURL: server.URL, OrchestratorClient: server.Client()}

		_, err := service.GetChatProgress(orchestratorTestContext(), "thread-123")
		server.Close()

		if !errors.Is(err, ErrChatProgressUnsupported) {
			t.Fatalf("status %d: expected ErrChatProgressUnsupported, got %v", status, err)
		}
	}
}

func TestGetChatProgressReportsUpstreamFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	service := &OrchestratorService{OrchestratorAPIURL: server.URL, OrchestratorClient: server.Client()}

	_, err := service.GetChatProgress(orchestratorTestContext(), "thread-123")

	if err == nil || errors.Is(err, ErrChatProgressUnsupported) {
		t.Fatalf("expected an upstream error, got %v", err)
	}
}
