package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
)

func TestListChatThreadsSendsTheSessionUserAndWorkspaceWithTheServiceToken(t *testing.T) {
	var gotPath, gotStudy, gotLimit, gotToken, gotUser, gotTenant, gotAuth, gotQueryUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotStudy, gotLimit = r.URL.Path, r.URL.Query().Get("study"), r.URL.Query().Get("limit")
		gotToken, gotUser, gotTenant = r.Header.Get("X-Service-Token"), r.Header.Get("X-Pacs-User-Id"), r.Header.Get("X-Pacs-Tenant-Id")
		gotAuth, gotQueryUser = r.Header.Get("Authorization"), r.URL.Query().Get("user_id")
		_, _ = w.Write([]byte(`{"threads":[{"thread_id":"t-1","message_count":2}]}`))
	}))
	defer server.Close()

	body, err := chatFeedbackService(server, "svc-token").ListChatThreads(orchestratorTestContext(), "1.2.3.4", 20)
	if err != nil {
		t.Fatalf("ListChatThreads: %v", err)
	}
	if string(body) != `{"threads":[{"thread_id":"t-1","message_count":2}]}` {
		t.Fatalf("unexpected body %s", body)
	}
	if gotPath != "/internal/chat-history/threads" || gotStudy != "1.2.3.4" || gotLimit != "20" {
		t.Fatalf("unexpected forward: path=%q study=%q limit=%q", gotPath, gotStudy, gotLimit)
	}
	if gotToken != "svc-token" || gotUser != "user-123" || gotTenant != "tenant-123" {
		t.Fatalf("unexpected identity: token=%q user=%q tenant=%q", gotToken, gotUser, gotTenant)
	}
	// The session token and a query user id are never sent to the history routes.
	if gotAuth != "" || gotQueryUser != "" {
		t.Fatalf("unexpected credentials forwarded: auth=%q user_id=%q", gotAuth, gotQueryUser)
	}
}

func TestGetChatThreadHistoryEscapesTheThreadAndMapsNotFound(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := chatFeedbackService(server, "svc-token").GetChatThreadHistory(orchestratorTestContext(), "t-1")
	if !errors.Is(err, ErrChatHistoryNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if gotPath != "/internal/chat-history/threads/t-1" {
		t.Fatalf("unexpected path %q", gotPath)
	}
}

func TestChatHistoryRefusesWithoutTokenOrSession(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	if _, err := chatFeedbackService(server, "").ListChatThreads(orchestratorTestContext(), "1.2.3", 0); !errors.Is(err, ErrChatHistoryNotConfigured) {
		t.Fatalf("want not configured, got %v", err)
	}
	// Without a session the answer is 401 even when the token is missing too.
	if _, err := chatFeedbackService(server, "").ListChatThreads(context.Background(), "1.2.3", 0); !errors.Is(err, ErrChatHistoryNoSession) {
		t.Fatalf("want no session, got %v", err)
	}
	noTenant := context.WithValue(context.Background(), iamTypes.UserIDCtx, "user-123")
	if _, err := chatFeedbackService(server, "svc-token").ListChatThreads(noTenant, "1.2.3", 0); !errors.Is(err, ErrChatHistoryNoSession) {
		t.Fatalf("want no session, got %v", err)
	}
	noUser := context.WithValue(context.Background(), iamTypes.TenantIDCtx, "tenant-123")
	if _, err := chatFeedbackService(server, "svc-token").GetChatThreadHistory(noUser, "t-1"); !errors.Is(err, ErrChatHistoryNoSession) {
		t.Fatalf("want no session, got %v", err)
	}
	if called {
		t.Fatal("the chat backend must not be called without a token and a session")
	}
}

func TestChatHistoryMapsBackendStatuses(t *testing.T) {
	status := http.StatusNotFound
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer server.Close()
	service := chatFeedbackService(server, "svc-token")

	// 404 on the list means an older chat backend without the routes.
	if _, err := service.ListChatThreads(orchestratorTestContext(), "1.2.3", 0); !errors.Is(err, ErrChatHistoryNotConfigured) {
		t.Fatalf("want not configured, got %v", err)
	}
	status = http.StatusBadRequest
	if _, err := service.ListChatThreads(orchestratorTestContext(), "1.2.3", 0); !errors.Is(err, ErrChatHistoryInvalid) {
		t.Fatalf("want invalid, got %v", err)
	}
	status = http.StatusUnauthorized
	if _, err := service.GetChatThreadHistory(orchestratorTestContext(), "t-1"); !errors.Is(err, ErrChatHistoryNotConfigured) {
		t.Fatalf("want not configured, got %v", err)
	}
}

func TestChatHistoryDoesNotFollowRedirects(t *testing.T) {
	elsewhere := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = true
	}))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer server.Close()

	if _, err := chatFeedbackService(server, "svc-token").ListChatThreads(orchestratorTestContext(), "1.2.3", 0); err == nil {
		t.Fatal("a redirect must not be treated as a history response")
	}
	if elsewhere {
		t.Fatal("the service token must not follow a redirect")
	}
}
