package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/module/orchestrator/application"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
	serviceTypes "api-pacs/module/orchestrator/infrastructure/service/types"
)

type chatMessageServiceStub struct {
	application.OrchestratorServiceInterface
	err error
}

func (stub *chatMessageServiceStub) CreateMessage(_ context.Context, _ serviceTypes.CreateMessageRequest) (serviceTypes.CreateMessageResponse, error) {
	return serviceTypes.CreateMessageResponse{}, stub.err
}

func chatMessageRequest(threadID string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/threads/thread/chat", bytes.NewBufferString(`{"message":"Summarize"}`))
	request.Header.Set("Content-Type", "application/json")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("threadID", threadID)
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}

func TestCreateMessageTellsTheUserWhyItFailedAndThatItCanBeRetried(t *testing.T) {
	tests := map[string]struct {
		err       error
		status    int
		errorCode string
		message   string
	}{
		"thread busy":       {orchestratorService.ErrChatTurnInProgress, http.StatusConflict, "chat_turn_in_progress", "still being written"},
		"chat backend 500":  {fmt.Errorf("%w: status 500", orchestratorService.ErrChatUpstream), http.StatusBadGateway, "chat_upstream_error", "try again"},
		"chat backend down": {errors.New("dial tcp 10.0.0.1:8585: connection refused"), http.StatusInternalServerError, "chat_request_failed", "try again"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			NewOrchestratorController(&chatMessageServiceStub{err: test.err}).CreateMessage(recorder, chatMessageRequest("thread-1"))

			require.Equal(t, test.status, recorder.Code)
			var body struct {
				Success   bool   `json:"success"`
				ErrorCode string `json:"errorCode"`
				Message   string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.False(t, body.Success)
			require.Equal(t, test.errorCode, body.ErrorCode)
			require.Contains(t, body.Message, test.message)
			require.NotContains(t, body.Message, "technical support")
			// Upstream error text (addresses, ports) is not echoed to the client.
			require.NotContains(t, recorder.Body.String(), "10.0.0.1")
		})
	}
}
