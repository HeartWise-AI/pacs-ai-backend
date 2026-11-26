package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/module/orchestrator/application"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
)

type chatHistoryServiceStub struct {
	application.OrchestratorServiceInterface
	study  string
	limit  int
	thread string
	body   json.RawMessage
	err    error
}

func (stub *chatHistoryServiceStub) ListChatThreads(_ context.Context, study string, limit int) (json.RawMessage, error) {
	stub.study, stub.limit = study, limit
	return stub.body, stub.err
}

func (stub *chatHistoryServiceStub) GetChatThreadHistory(_ context.Context, threadID string) (json.RawMessage, error) {
	stub.thread = threadID
	return stub.body, stub.err
}

func chatHistoryThreadRequest(threadID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/threads/x/messages", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("threadID", threadID)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}

func TestListChatThreadsValidatesTheStudyAndLimit(t *testing.T) {
	stub := &chatHistoryServiceStub{body: json.RawMessage(`{"threads":[]}`)}
	controller := NewOrchestratorController(stub)

	recorder := httptest.NewRecorder()
	controller.ListChatThreads(recorder, httptest.NewRequest(http.MethodGet, "/threads?studyInstanceUID=1.2.840.1&limit=5&user_id=someone-else", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "1.2.840.1", stub.study)
	require.Equal(t, 5, stub.limit)
	require.JSONEq(t, `{"threads":[]}`, string(mustData(t, recorder)))

	for _, target := range []string{
		"/threads",
		"/threads?studyInstanceUID=",
		"/threads?studyInstanceUID=1.2%20OR%201=1",
		"/threads?studyInstanceUID=1.2.3&limit=0",
		"/threads?studyInstanceUID=1.2.3&limit=51",
		"/threads?studyInstanceUID=1.2.3&limit=abc",
	} {
		recorder := httptest.NewRecorder()
		controller.ListChatThreads(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		require.Equal(t, http.StatusBadRequest, recorder.Code, target)
	}
}

func TestGetChatThreadMessagesMapsErrors(t *testing.T) {
	stub := &chatHistoryServiceStub{body: json.RawMessage(`{"messages":[]}`)}
	controller := NewOrchestratorController(stub)

	recorder := httptest.NewRecorder()
	controller.GetChatThreadMessages(recorder, chatHistoryThreadRequest("thread-1"))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "thread-1", stub.thread)

	recorder = httptest.NewRecorder()
	controller.GetChatThreadMessages(recorder, chatHistoryThreadRequest("../etc"))
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	for err, status := range map[error]int{
		orchestratorService.ErrChatHistoryNotFound:      http.StatusNotFound,
		orchestratorService.ErrChatHistoryNoSession:     http.StatusUnauthorized,
		orchestratorService.ErrChatHistoryNotConfigured: http.StatusServiceUnavailable,
		context.DeadlineExceeded:                        http.StatusBadGateway,
	} {
		stub.err = err
		recorder := httptest.NewRecorder()
		controller.GetChatThreadMessages(recorder, chatHistoryThreadRequest("thread-1"))
		require.Equal(t, status, recorder.Code, err.Error())
	}
}

func mustData(t *testing.T, recorder *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body.Data
}
