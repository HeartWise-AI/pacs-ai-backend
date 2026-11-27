package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"api-pacs/module/orchestrator/infrastructure/service/types"
)

// Chat feedback (PACS-448): thumbs on chat answers. A clinician's vote goes to
// the chat backend with their session, like a chat message; the admin panel
// reads every vote of the workspace through the chat backend's service routes
// with ChatFeedbackServiceToken, after the router checked feedback.view.

// ErrChatFeedbackNotConfigured: CHAT_FEEDBACK_SERVICE_TOKEN is not set here
// or the chat backend has no review routes.
var ErrChatFeedbackNotConfigured = errors.New("chat_feedback_not_configured")

// ErrChatFeedbackNotFound: no such conversation, answer or vote for the caller.
var ErrChatFeedbackNotFound = errors.New("chat_feedback_not_found")

// ErrChatFeedbackInvalid: the chat backend refused the vote or the filters.
var ErrChatFeedbackInvalid = errors.New("chat_feedback_invalid")

const chatFeedbackTimeout = 20 * time.Second

// ChatFeedbackQueryKeys are the only filters forwarded to the chat backend.
var ChatFeedbackQueryKeys = []string{"rating", "category", "study", "from", "to", "days", "limit", "offset"}

func (service *OrchestratorService) feedbackURL(threadID, messageID string) string {
	return fmt.Sprintf("%s/chat/%s/messages/%s/feedback", service.OrchestratorAPIURL, url.PathEscape(threadID), url.PathEscape(messageID))
}

// userRequest builds a request on behalf of the signed-in user: session token
// in the Authorization header, user and workspace in the query.
func (service *OrchestratorService) userRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("user_id", service.extractUserIDFromContext(ctx))
	if tenantID := service.extractTenantIDFromContext(ctx); tenantID != "" {
		q.Set("tenant_id", tenantID)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+service.extractBearerTokenFromContext(ctx))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func chatFeedbackStatusError(status int) error {
	switch {
	case status == http.StatusNotFound:
		return ErrChatFeedbackNotFound
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ErrChatFeedbackInvalid
	case status == http.StatusServiceUnavailable || status == http.StatusUnauthorized:
		return ErrChatFeedbackNotConfigured
	default:
		return fmt.Errorf("chat backend returned status: %d", status)
	}
}

// SubmitChatFeedback creates or replaces the caller's vote on one answer.
func (service *OrchestratorService) SubmitChatFeedback(ctx context.Context, threadID, messageID string, vote types.ChatFeedbackVote) (map[string]interface{}, error) {
	payload, err := json.Marshal(vote)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, chatFeedbackTimeout)
	defer cancel()
	req, err := service.userRequest(ctx, http.MethodPut, service.feedbackURL(threadID, messageID), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, chatFeedbackStatusError(resp.StatusCode)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveChatFeedback deletes the caller's vote on one answer.
func (service *OrchestratorService) RemoveChatFeedback(ctx context.Context, threadID, messageID string) error {
	ctx, cancel := context.WithTimeout(ctx, chatFeedbackTimeout)
	defer cancel()
	req, err := service.userRequest(ctx, http.MethodDelete, service.feedbackURL(threadID, messageID), nil)
	if err != nil {
		return err
	}
	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return chatFeedbackStatusError(resp.StatusCode)
	}
	return nil
}

// ListChatFeedback returns the workspace's votes on chat answers (admin panel).
func (service *OrchestratorService) ListChatFeedback(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return service.chatFeedbackReview(ctx, "list", query)
}

// CountChatFeedback returns the workspace's vote counts (admin panel).
func (service *OrchestratorService) CountChatFeedback(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return service.chatFeedbackReview(ctx, "counts", query)
}

// GetChatFeedback returns one vote of the workspace with the redacted tool
// calls of the rated answer (admin panel).
func (service *OrchestratorService) GetChatFeedback(ctx context.Context, feedbackID string) (json.RawMessage, error) {
	return service.chatFeedbackReview(ctx, url.PathEscape(feedbackID), nil)
}

func (service *OrchestratorService) chatFeedbackReview(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if service.ChatFeedbackServiceToken == "" {
		return nil, ErrChatFeedbackNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, chatFeedbackTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/internal/chat-feedback/%s", service.OrchestratorAPIURL, path), nil)
	if err != nil {
		return nil, err
	}
	forwarded := url.Values{}
	for _, key := range ChatFeedbackQueryKeys {
		if value := query.Get(key); value != "" {
			forwarded.Set(key, value)
		}
	}
	req.URL.RawQuery = forwarded.Encode()
	req.Header.Set("X-Service-Token", service.ChatFeedbackServiceToken)
	req.Header.Set("X-Pacs-User-Id", service.extractUserIDFromContext(ctx))
	if tenantID := service.extractTenantIDFromContext(ctx); tenantID != "" {
		req.Header.Set("X-Pacs-Tenant-Id", tenantID)
	}
	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && path != "list" && path != "counts" {
		// One vote: the chat backend says it does not exist in this workspace.
		return nil, ErrChatFeedbackNotFound
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// An older chat backend without the review routes.
		return nil, ErrChatFeedbackNotConfigured
	}
	if resp.StatusCode != http.StatusOK {
		return nil, chatFeedbackStatusError(resp.StatusCode)
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
