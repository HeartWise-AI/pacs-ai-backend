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

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
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

type chatFeedbackOneStub struct {
	chatFeedbackServiceStub
	gotID string
	one   json.RawMessage
	err   error
}

func (stub *chatFeedbackOneStub) GetChatFeedback(_ context.Context, feedbackID string) (json.RawMessage, error) {
	stub.gotID = feedbackID
	return stub.one, stub.err
}

type fakeChatFeedbackUsers struct {
	calls int
	err   error
}

func (users *fakeChatFeedbackUsers) list(_ context.Context, tenantID string) (map[string]ChatFeedbackUser, error) {
	users.calls++
	if users.err != nil {
		return nil, users.err
	}
	if tenantID != "tenant-a" {
		return map[string]ChatFeedbackUser{}, nil
	}
	return map[string]ChatFeedbackUser{"uid-alice": {Name: "Alice Martin", Email: "alice@example.org"}}, nil
}

func chatFeedbackOneRequest(feedbackID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/chat/feedback/"+feedbackID, nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("feedbackID", feedbackID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, "tenant-a")
	return request.WithContext(ctx)
}

func TestGetChatFeedbackForwardsTheIDAndNamesTheVoter(t *testing.T) {
	stub := &chatFeedbackOneStub{one: json.RawMessage(`{"feedback_id":"f-1","user_id":"uid-alice","tool_calls":[{"tool":"task"}]}`)}
	users := &fakeChatFeedbackUsers{}
	controller := NewOrchestratorController(stub)
	controller.ChatFeedbackUsers = &CachedChatFeedbackUsers{List: users.list}

	recorder := httptest.NewRecorder()
	controller.GetChatFeedback(recorder, chatFeedbackOneRequest("f-1"))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "f-1", stub.gotID)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var body struct {
		Data struct {
			UserName  string                   `json:"user_name"`
			UserEmail string                   `json:"user_email"`
			ToolCalls []map[string]interface{} `json:"tool_calls"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "Alice Martin", body.Data.UserName)
	require.Equal(t, "alice@example.org", body.Data.UserEmail)
	require.Len(t, body.Data.ToolCalls, 1)

	// The directory is cached per workspace.
	controller.GetChatFeedback(httptest.NewRecorder(), chatFeedbackOneRequest("f-1"))
	require.Equal(t, 1, users.calls)

	recorder = httptest.NewRecorder()
	controller.GetChatFeedback(recorder, chatFeedbackOneRequest("../x"))
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	stub.err = orchestratorService.ErrChatFeedbackNotFound
	recorder = httptest.NewRecorder()
	controller.GetChatFeedback(recorder, chatFeedbackOneRequest("f-2"))
	require.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestListChatFeedbackNamesVotersAndPassesThroughWhenTheDirectoryFails(t *testing.T) {
	stub := &chatFeedbackServiceStub{listBody: json.RawMessage(`{"total":2,"items":[{"user_id":"uid-alice"},{"user_id":"uid-unknown"}]}`)}
	users := &fakeChatFeedbackUsers{}
	controller := NewOrchestratorController(stub)
	controller.ChatFeedbackUsers = &CachedChatFeedbackUsers{List: users.list}
	request := httptest.NewRequest(http.MethodGet, "/chat/feedback/list", nil)
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a"))

	recorder := httptest.NewRecorder()
	controller.ListChatFeedback(recorder, request)
	require.Contains(t, recorder.Body.String(), `"user_name":"Alice Martin"`)
	require.NotContains(t, recorder.Body.String(), `"user_name":""`)

	failing := NewOrchestratorController(stub)
	failing.ChatFeedbackUsers = &CachedChatFeedbackUsers{List: (&fakeChatFeedbackUsers{err: context.DeadlineExceeded}).list}
	recorder = httptest.NewRecorder()
	failing.ListChatFeedback(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "user_name")
	require.Contains(t, recorder.Body.String(), `"uid-alice"`)
}
