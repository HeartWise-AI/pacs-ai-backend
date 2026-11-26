package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/orchestrator/application"
	serviceTypes "api-pacs/module/orchestrator/infrastructure/service/types"
)

type chatSettingsServiceStub struct {
	application.OrchestratorServiceInterface
	response serviceTypes.ChatSettingsResponse
	err      error
}

func (stub *chatSettingsServiceStub) GetChatSettings(_ context.Context) (serviceTypes.ChatSettingsResponse, error) {
	return stub.response, stub.err
}

func TestGetChatSettingsReturnsTheDefaultLanguage(t *testing.T) {
	language := "fr"
	recorder := httptest.NewRecorder()
	NewOrchestratorController(&chatSettingsServiceStub{response: serviceTypes.ChatSettingsResponse{ChatDefaultLanguage: &language}}).
		GetChatSettings(recorder, httptest.NewRequest(http.MethodGet, "/v1/orchestrator/settings", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			ChatDefaultLanguage *string `json:"chat_default_language"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.True(t, body.Success)
	require.Equal(t, "fr", *body.Data.ChatDefaultLanguage)
}

func TestGetChatSettingsIsABadGatewayWhenTheChatBackendFails(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewOrchestratorController(&chatSettingsServiceStub{err: errors.New("down")}).
		GetChatSettings(recorder, httptest.NewRequest(http.MethodGet, "/v1/orchestrator/settings", nil))
	require.Equal(t, http.StatusBadGateway, recorder.Code)
}
