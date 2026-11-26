package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func chatSettingsService(t *testing.T, status int, body string) *OrchestratorService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" || r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("no credentials should reach the public version endpoint")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &OrchestratorService{OrchestratorAPIURL: server.URL, OrchestratorClient: server.Client()}
}

func TestGetChatSettingsReturnsTheDefaultLanguage(t *testing.T) {
	service := chatSettingsService(t, http.StatusOK, `{"git_sha":"abc","chat_default_language":"fr"}`)
	settings, err := service.GetChatSettings(context.Background())
	if err != nil || settings.ChatDefaultLanguage == nil || *settings.ChatDefaultLanguage != "fr" {
		t.Fatalf("got %+v, %v", settings, err)
	}
}

func TestGetChatSettingsIsNullForAnOlderChatBackend(t *testing.T) {
	for _, body := range []string{`{"git_sha":"abc"}`, `{"chat_default_language":"de"}`} {
		settings, err := chatSettingsService(t, http.StatusOK, body).GetChatSettings(context.Background())
		if err != nil || settings.ChatDefaultLanguage != nil {
			t.Fatalf("%s: got %+v, %v", body, settings, err)
		}
	}
}

func TestGetChatSettingsFailsWhenTheChatBackendFails(t *testing.T) {
	if _, err := chatSettingsService(t, http.StatusBadGateway, `{}`).GetChatSettings(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}
