package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/module/orchestrator/application"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
	serviceTypes "api-pacs/module/orchestrator/infrastructure/service/types"
)

type chatFeedbackServiceStub struct {
	application.OrchestratorServiceInterface
	votes    []serviceTypes.ChatFeedbackVote
	listErr  error
	listBody json.RawMessage
}

func (stub *chatFeedbackServiceStub) SubmitChatFeedback(_ context.Context, _, _ string, vote serviceTypes.ChatFeedbackVote) (map[string]interface{}, error) {
	stub.votes = append(stub.votes, vote)
	return map[string]interface{}{"ok": true}, nil
}

func (stub *chatFeedbackServiceStub) ListChatFeedback(_ context.Context, _ url.Values) (json.RawMessage, error) {
	return stub.listBody, stub.listErr
}

func chatFeedbackVoteRequest(threadID, messageID, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPut, "/feedback", strings.NewReader(body))
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("threadID", threadID)
	routeContext.URLParams.Add("messageID", messageID)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}

func TestSubmitChatFeedbackValidatesAndForwards(t *testing.T) {
	stub := &chatFeedbackServiceStub{}
	controller := NewOrchestratorController(stub)

	recorder := httptest.NewRecorder()
	controller.SubmitChatFeedback(recorder, chatFeedbackVoteRequest("thread-1", "answer-1", `{"vote":"down","category":"unsupported_claim","comment":"no source"}`))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, stub.votes, 1)
	require.Equal(t, "down", stub.votes[0].Vote)
	require.Equal(t, "web", stub.votes[0].Client)
	require.Equal(t, "no source", *stub.votes[0].Comment)

	for _, testCase := range []struct{ thread, message, body string }{
		{"thread-1", "answer-1", `{"vote":"maybe","category":"other"}`},
		{"thread-1", "answer-1", `{"vote":"up"}`},
		{"thread-1", "answer-1", `{"vote":"up","category":"` + strings.Repeat("x", 41) + `"}`},
		{"thread-1", "answer-1", `{"vote":"down","category":"other","comment":"` + strings.Repeat("x", 5001) + `"}`},
		{"../etc", "answer-1", `{"vote":"up","category":"accurate"}`},
		{"thread-1", "a/b", `{"vote":"up","category":"accurate"}`},
	} {
		recorder := httptest.NewRecorder()
		controller.SubmitChatFeedback(recorder, chatFeedbackVoteRequest(testCase.thread, testCase.message, testCase.body))
		require.Equal(t, http.StatusBadRequest, recorder.Code, testCase.body)
	}
	require.Len(t, stub.votes, 1)
}

func TestListChatFeedbackPassesTheBackendJSONThrough(t *testing.T) {
	stub := &chatFeedbackServiceStub{listBody: json.RawMessage(`{"total":1,"items":[{"vote":"down"}]}`)}
	recorder := httptest.NewRecorder()
	NewOrchestratorController(stub).ListChatFeedback(recorder, httptest.NewRequest(http.MethodGet, "/chat/feedback/list?rating=down", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, 1, body.Data.Total)

	stub.listErr = orchestratorService.ErrChatFeedbackNotConfigured
	recorder = httptest.NewRecorder()
	NewOrchestratorController(stub).ListChatFeedback(recorder, httptest.NewRequest(http.MethodGet, "/chat/feedback/list", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
