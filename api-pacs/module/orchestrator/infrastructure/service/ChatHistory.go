package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Chat history per study: the signed-in user's earlier viewer chats about the
// open study, so the chat box can restore and continue them. The chat backend
// is called with the shared service token and the user and workspace of the
// authenticated session, never with ids supplied by the browser.

// ErrChatHistoryNotFound: no such thread for the signed-in user and workspace.
var ErrChatHistoryNotFound = errors.New("chat_history_not_found")

// ErrChatHistoryInvalid: the chat backend refused the study identifier.
var ErrChatHistoryInvalid = errors.New("chat_history_invalid")

// ErrChatHistoryNotConfigured: no service token here, or an older chat backend.
var ErrChatHistoryNotConfigured = errors.New("chat_history_not_configured")

// ErrChatHistoryNoSession: the request carries no user or workspace.
var ErrChatHistoryNoSession = errors.New("chat_history_no_session")

// ListChatThreads lists the caller's threads about one study, latest first.
func (service *OrchestratorService) ListChatThreads(ctx context.Context, studyInstanceUID string, limit int) (json.RawMessage, error) {
	query := url.Values{}
	query.Set("study", studyInstanceUID)
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return service.chatHistory(ctx, "threads", query)
}

// GetChatThreadHistory returns one of the caller's threads: messages and votes.
func (service *OrchestratorService) GetChatThreadHistory(ctx context.Context, threadID string) (json.RawMessage, error) {
	return service.chatHistory(ctx, "threads/"+url.PathEscape(threadID), nil)
}

func (service *OrchestratorService) chatHistory(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if service.ChatFeedbackServiceToken == "" {
		return nil, ErrChatHistoryNotConfigured
	}
	userID := service.extractUserIDFromContext(ctx)
	tenantID := service.extractTenantIDFromContext(ctx)
	if userID == "" || tenantID == "" {
		return nil, ErrChatHistoryNoSession
	}
	ctx, cancel := context.WithTimeout(ctx, chatFeedbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/internal/chat-history/%s", service.OrchestratorAPIURL, path), nil)
	if err != nil {
		return nil, err
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	req.Header.Set("X-Service-Token", service.ChatFeedbackServiceToken)
	req.Header.Set("X-Pacs-User-Id", userID)
	req.Header.Set("X-Pacs-Tenant-Id", tenantID)
	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound && path != "threads":
		return nil, ErrChatHistoryNotFound
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		// An older chat backend without the history routes.
		return nil, ErrChatHistoryNotConfigured
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity:
		return nil, ErrChatHistoryInvalid
	case resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrChatHistoryNotConfigured
	default:
		return nil, fmt.Errorf("chat backend returned status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errors.New("chat backend returned invalid JSON")
	}
	return json.RawMessage(body), nil
}
