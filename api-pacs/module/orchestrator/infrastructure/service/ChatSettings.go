package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"api-pacs/module/orchestrator/infrastructure/service/types"
)

// GetChatSettings reads the chat backend's public /version (no credentials are sent).
func (service *OrchestratorService) GetChatSettings(ctx context.Context) (types.ChatSettingsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, chatProgressTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/version", service.OrchestratorAPIURL), nil)
	if err != nil {
		return types.ChatSettingsResponse{}, err
	}
	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return types.ChatSettingsResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return types.ChatSettingsResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	var version struct {
		ChatDefaultLanguage string `json:"chat_default_language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return types.ChatSettingsResponse{}, err
	}
	if version.ChatDefaultLanguage != "fr" && version.ChatDefaultLanguage != "en" {
		return types.ChatSettingsResponse{}, nil
	}
	language := version.ChatDefaultLanguage
	return types.ChatSettingsResponse{ChatDefaultLanguage: &language}, nil
}
