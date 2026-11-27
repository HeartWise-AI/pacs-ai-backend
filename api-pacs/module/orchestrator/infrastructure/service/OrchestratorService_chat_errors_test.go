package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"api-pacs/module/orchestrator/domain/entity"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

func chatErrorTestService(t *testing.T, status int, body string) *OrchestratorService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &OrchestratorService{
		OrchestratorAPIURL: server.URL,
		OrchestratorClient: server.Client(),
		ThreadsByID:        map[string]*entity.Thread{},
	}
}

// Seen live: a second question while the first answer of the thread was still
// running got the chat backend's 409, shown as "Please contact technical support".
func TestCreateMessageReportsABusyThreadAsTurnInProgress(t *testing.T) {
	service := chatErrorTestService(t, http.StatusConflict, `{"detail":"A request is already running for this conversation"}`)

	_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{ThreadID: "thread-1", Content: "again"})

	if !errors.Is(err, ErrChatTurnInProgress) {
		t.Fatalf("expected ErrChatTurnInProgress, got %v", err)
	}
}

func TestCreateMessageReportsOtherChatBackendFailuresAsUpstreamErrors(t *testing.T) {
	service := chatErrorTestService(t, http.StatusInternalServerError, `{"detail":"boom"}`)

	_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{ThreadID: "thread-1", Content: "q"})

	if !errors.Is(err, ErrChatUpstream) || errors.Is(err, ErrChatTurnInProgress) {
		t.Fatalf("expected ErrChatUpstream, got %v", err)
	}
}

// A timed-out or failed turn comes back answered (a stored, votable message):
// it reaches the viewer as an answer with its status and id, not as an error.
func TestCreateMessagePassesAnAnsweredFailedTurnThrough(t *testing.T) {
	for _, status := range []string{"timeout", "error"} {
		t.Run(status, func(t *testing.T) {
			service := chatErrorTestService(t, http.StatusOK, `{"conversation_id":"thread-1","status":"`+status+`",
				"message":{"role":"assistant","content":"This answer could not be completed. Ask it again."},"message_id":"answer-1"}`)

			response, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{ThreadID: "thread-1", Content: "q"})

			if err != nil {
				t.Fatalf("CreateMessage returned error: %v", err)
			}
			if response.Status != status || response.AssistantMessageID != "answer-1" || response.Response == "" {
				t.Fatalf("unexpected response: status=%q id=%q content=%q", response.Status, response.AssistantMessageID, response.Response)
			}
		})
	}
}

func TestCreateMessageStillFailsANonSuccessStatusWithoutAnswer(t *testing.T) {
	service := chatErrorTestService(t, http.StatusOK, `{"conversation_id":"thread-1","status":"error"}`)

	_, err := service.CreateMessage(orchestratorTestContext(), types.CreateMessageRequest{ThreadID: "thread-1", Content: "q"})

	if !errors.Is(err, ErrChatUpstream) {
		t.Fatalf("expected ErrChatUpstream, got %v", err)
	}
}
