package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"api-pacs/module/orchestrator/domain/entity"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

func chatFeedbackService(server *httptest.Server, token string) *OrchestratorService {
	return &OrchestratorService{
		OrchestratorAPIURL:       server.URL,
		OrchestratorClient:       server.Client(),
		ThreadsByID:              map[string]*entity.Thread{},
		ChatFeedbackServiceToken: token,
	}
}

func TestSubmitChatFeedbackForwardsTheVoteWithTheUserSession(t *testing.T) {
	var gotPath, gotAuth, gotUser, gotTenant string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		gotUser, gotTenant = r.URL.Query().Get("user_id"), r.URL.Query().Get("tenant_id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"feedback_id":"fb-1","vote":"down"}`))
	}))
	defer server.Close()

	comment := "wrong vessel"
	out, err := chatFeedbackService(server, "").SubmitChatFeedback(orchestratorTestContext(), "thread-1", "answer-1", types.ChatFeedbackVote{Vote: "down", Category: "other", Comment: &comment, Client: "web"})
	if err != nil {
		t.Fatalf("SubmitChatFeedback: %v", err)
	}
	if out["feedback_id"] != "fb-1" {
		t.Fatalf("unexpected response %#v", out)
	}
	if gotPath != "/chat/thread-1/messages/answer-1/feedback" || gotAuth != "Bearer session-token-123" || gotUser != "user-123" || gotTenant != "tenant-123" {
		t.Fatalf("unexpected forward: path=%q auth=%q user=%q tenant=%q", gotPath, gotAuth, gotUser, gotTenant)
	}
	if gotBody["vote"] != "down" || gotBody["category"] != "other" || gotBody["comment"] != "wrong vessel" || gotBody["client"] != "web" {
		t.Fatalf("unexpected body %#v", gotBody)
	}
}

func TestChatFeedbackMapsBackendErrors(t *testing.T) {
	status := http.StatusNotFound
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer server.Close()
	service := chatFeedbackService(server, "")
	if err := service.RemoveChatFeedback(orchestratorTestContext(), "thread-1", "answer-1"); !errors.Is(err, ErrChatFeedbackNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	status = http.StatusBadRequest
	if _, err := service.SubmitChatFeedback(orchestratorTestContext(), "thread-1", "answer-1", types.ChatFeedbackVote{Vote: "up", Category: "x"}); !errors.Is(err, ErrChatFeedbackInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
}

func TestListChatFeedbackUsesTheServiceTokenAndOnlyKnownFilters(t *testing.T) {
	var gotPath, gotToken, gotUser, gotTenant, gotAuth string
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		gotToken, gotUser, gotTenant, gotAuth = r.Header.Get("X-Service-Token"), r.Header.Get("X-Pacs-User-Id"), r.Header.Get("X-Pacs-Tenant-Id"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"total":1,"items":[{"vote":"down"}]}`))
	}))
	defer server.Close()

	query := url.Values{"rating": {"down"}, "study": {"1.2.3"}, "from": {"2026-11-01"}, "limit": {"20"}, "user_id": {"someone-else"}}
	raw, err := chatFeedbackService(server, "svc-token").ListChatFeedback(orchestratorTestContext(), query)
	if err != nil {
		t.Fatalf("ListChatFeedback: %v", err)
	}
	if string(raw) != `{"total":1,"items":[{"vote":"down"}]}` {
		t.Fatalf("unexpected body %s", raw)
	}
	if gotPath != "/internal/chat-feedback/list" || gotToken != "svc-token" || gotUser != "user-123" || gotTenant != "tenant-123" || gotAuth != "" {
		t.Fatalf("unexpected forward: path=%q token=%q user=%q tenant=%q auth=%q", gotPath, gotToken, gotUser, gotTenant, gotAuth)
	}
	if gotQuery.Get("rating") != "down" || gotQuery.Get("study") != "1.2.3" || gotQuery.Get("from") != "2026-11-01" || gotQuery.Get("limit") != "20" || gotQuery.Has("user_id") {
		t.Fatalf("unexpected query %v", gotQuery)
	}
}

func TestChatFeedbackReviewNeedsTheToken(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer server.Close()
	if _, err := chatFeedbackService(server, "").CountChatFeedback(orchestratorTestContext(), url.Values{}); !errors.Is(err, ErrChatFeedbackNotConfigured) {
		t.Fatalf("want not configured, got %v", err)
	}
	if called {
		t.Fatal("the chat backend must not be called without a token")
	}
}

func TestCreateMessageReturnsTheStoredAnswerID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"thread_id":"thread-1","status":"success","message":{"role":"assistant","content":"ok"},"message_id":"answer-uuid"}`))
	}))
	defer server.Close()
	response, err := chatFeedbackService(server, "").CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{ThreadID: "thread-1", Content: "hi"})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if response.AssistantMessageID != "answer-uuid" {
		t.Fatalf("unexpected assistant message id %q", response.AssistantMessageID)
	}
}
