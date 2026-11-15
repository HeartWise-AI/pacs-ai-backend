package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/module/orchestrator/application"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
	serviceTypes "api-pacs/module/orchestrator/infrastructure/service/types"
)

type chatProgressServiceStub struct {
	application.OrchestratorServiceInterface
	threadIDs []string
	response  serviceTypes.ChatProgressResponse
	err       error
}

func (stub *chatProgressServiceStub) GetChatProgress(_ context.Context, threadID string) (serviceTypes.ChatProgressResponse, error) {
	stub.threadIDs = append(stub.threadIDs, threadID)
	return stub.response, stub.err
}

func chatProgressRequest(threadID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/orchestrator/threads/thread/chat/progress", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("threadID", threadID)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}

func TestGetChatProgressReturnsTheStepsSoFar(t *testing.T) {
	model := "DeepCORO-JEPA"
	duration := int64(140)
	ended := "2026-11-14T22:27:29.140Z"
	started := "2026-11-14T22:27:20.000Z"
	stub := &chatProgressServiceStub{response: serviceTypes.ChatProgressResponse{
		ThreadID: "6ac4f6c8-3d6f-4dad-a1ee-f1f415f7c41b",
		Active:   true,
		ChatActivity: serviceTypes.ChatActivity{
			Status:    "running",
			StartedAt: &started,
			ElapsedMs: 9120,
			Steps: []serviceTypes.ChatActivityStep{
				{ID: 1, Key: "model_result", Kind: "tool", Label: "Reading the stored DeepCORO-JEPA result", Model: &model, Status: "done", StartedAt: "2026-11-14T22:27:29.000Z", EndedAt: &ended, DurationMs: &duration},
				{ID: 2, Key: "web_search", Kind: "tool", Label: "Searching the web", Status: "running", StartedAt: "2026-11-14T22:27:29.200Z"},
			},
		},
	}}
	recorder := httptest.NewRecorder()

	NewOrchestratorController(stub).GetChatProgress(recorder, chatProgressRequest("6ac4f6c8-3d6f-4dad-a1ee-f1f415f7c41b"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []string{"6ac4f6c8-3d6f-4dad-a1ee-f1f415f7c41b"}, stub.threadIDs)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			ThreadID  string `json:"thread_id"`
			Active    bool   `json:"active"`
			Status    string `json:"status"`
			ElapsedMs int64  `json:"elapsed_ms"`
			Steps     []struct {
				ID         int     `json:"id"`
				Key        string  `json:"key"`
				Label      string  `json:"label"`
				Model      *string `json:"model"`
				Status     string  `json:"status"`
				EndedAt    *string `json:"ended_at"`
				DurationMs *int64  `json:"duration_ms"`
			} `json:"steps"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.True(t, body.Data.Active)
	require.Equal(t, "running", body.Data.Status)
	require.Equal(t, int64(9120), body.Data.ElapsedMs)
	require.Len(t, body.Data.Steps, 2)
	require.Equal(t, "DeepCORO-JEPA", *body.Data.Steps[0].Model)
	require.Equal(t, int64(140), *body.Data.Steps[0].DurationMs)
	require.Equal(t, "running", body.Data.Steps[1].Status)
	require.Nil(t, body.Data.Steps[1].Model)
	require.Nil(t, body.Data.Steps[1].EndedAt)
	require.Nil(t, body.Data.Steps[1].DurationMs)
}

func TestGetChatProgressRejectsThreadIDsThatAreNotThreadIDs(t *testing.T) {
	for _, threadID := range []string{"", "../admin", "thread/..", "a b", "thread?x=1", "thread%2F..", strings.Repeat("a", 129)} {
		stub := &chatProgressServiceStub{}
		recorder := httptest.NewRecorder()

		NewOrchestratorController(stub).GetChatProgress(recorder, chatProgressRequest(threadID))

		require.Equal(t, http.StatusBadRequest, recorder.Code, threadID)
		require.Empty(t, stub.threadIDs, threadID)
	}
}

func TestGetChatProgressTellsAnOlderChatBackendApartFromAnOutage(t *testing.T) {
	tests := map[string]struct {
		err       error
		status    int
		errorCode string
	}{
		"older chat backend": {orchestratorService.ErrChatProgressUnsupported, http.StatusNotFound, "chat_progress_unsupported"},
		"chat backend down":  {errors.New("dial tcp 10.0.0.1:8585: connection refused"), http.StatusBadGateway, "chat_progress_unavailable"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			NewOrchestratorController(&chatProgressServiceStub{err: test.err}).GetChatProgress(recorder, chatProgressRequest("thread-1"))

			require.Equal(t, test.status, recorder.Code)
			var body struct {
				Success   bool   `json:"success"`
				ErrorCode string `json:"errorCode"`
				Message   string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.False(t, body.Success)
			require.Equal(t, test.errorCode, body.ErrorCode)
			// Upstream error text (addresses, ports) is not echoed to the client.
			require.NotContains(t, recorder.Body.String(), "10.0.0.1")
		})
	}
}

func TestOpenAPIDocumentsChatProgress(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	payload, err := os.ReadFile(filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../docs/openapi.json")))
	require.NoError(t, err)
	var document struct {
		Paths map[string]map[string]struct {
			Security  []map[string][]string  `json:"security"`
			Responses map[string]interface{} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]interface{} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(payload, &document))

	operation, ok := document.Paths["/orchestrator/threads/{threadID}/chat/progress"]["get"]
	require.True(t, ok)
	require.Contains(t, operation.Security[0], "bearerAuth")
	for _, status := range []string{"200", "400", "401", "404", "502"} {
		require.Contains(t, operation.Responses, status)
	}
	for _, field := range []string{"id", "key", "kind", "label", "model", "status", "started_at", "ended_at", "duration_ms"} {
		require.Contains(t, document.Components.Schemas["ChatActivityStep"].Properties, field)
	}
	require.Contains(t, document.Components.Schemas["ChatActivity"].Properties, "steps")
	require.Contains(t, document.Components.Schemas, "ChatProgressResponse")
}
