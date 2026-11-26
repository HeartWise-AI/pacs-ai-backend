package rest

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	apiError "api-pacs/internal/errors"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
)

// Chat history per study:
// GET /v1/orchestrator/threads?studyInstanceUID=<uid>[&limit=n]  the caller's threads about the study
// GET /v1/orchestrator/threads/{threadID}/messages              one of them: messages and votes
// Same session and policy guards as the other orchestrator routes. The user and
// workspace come from the session; a user id in the query is ignored.

const (
	chatHistoryMaxStudyUIDLength = 128
	chatHistoryMaxLimit          = 50
)

func writeChatHistoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orchestratorService.ErrChatHistoryNotFound):
		writeChatFeedback(w, http.StatusNotFound, "Conversation not found.", err.Error(), nil)
	case errors.Is(err, orchestratorService.ErrChatHistoryInvalid):
		writeChatFeedback(w, http.StatusBadRequest, "Invalid study identifier.", err.Error(), nil)
	case errors.Is(err, orchestratorService.ErrChatHistoryNoSession):
		writeChatFeedback(w, http.StatusUnauthorized, "Unauthorized.", err.Error(), nil)
	case errors.Is(err, orchestratorService.ErrChatHistoryNotConfigured):
		writeChatFeedback(w, http.StatusServiceUnavailable, "Chat history is not available.", err.Error(), nil)
	default:
		writeChatFeedback(w, http.StatusBadGateway, "Chat history is unavailable.", "chat_history_unavailable", nil)
	}
}

func validStudyInstanceUID(value string) bool {
	if value == "" || len(value) > chatHistoryMaxStudyUIDLength {
		return false
	}
	for _, character := range value {
		if !(character == '.' || character == '-' || character == '_' || (character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')) {
			return false
		}
	}
	return true
}

// ListChatThreads lists the signed-in user's chat threads about one study, latest first.
func (controller *OrchestratorController) ListChatThreads(w http.ResponseWriter, r *http.Request) {
	study := strings.TrimSpace(r.URL.Query().Get("studyInstanceUID"))
	if !validStudyInstanceUID(study) {
		writeChatFeedback(w, http.StatusBadRequest, "studyInstanceUID is required.", apiError.InvalidRequestPayload, nil)
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > chatHistoryMaxLimit {
			writeChatFeedback(w, http.StatusBadRequest, "limit must be between 1 and 50.", apiError.InvalidRequestPayload, nil)
			return
		}
		limit = parsed
	}
	result, err := controller.OrchestratorServiceInterface.ListChatThreads(r.Context(), study, limit)
	if err != nil {
		writeChatHistoryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeChatFeedback(w, http.StatusOK, "Chat threads retrieved successfully.", "", result)
}

// GetChatThreadMessages returns one of the signed-in user's threads with its messages and votes.
func (controller *OrchestratorController) GetChatThreadMessages(w http.ResponseWriter, r *http.Request) {
	threadID := chi.URLParam(r, "threadID")
	if !orchestratorThreadIDPattern.MatchString(threadID) {
		writeChatFeedback(w, http.StatusBadRequest, "Thread ID is invalid.", apiError.InvalidRequestPayload, nil)
		return
	}
	result, err := controller.OrchestratorServiceInterface.GetChatThreadHistory(r.Context(), threadID)
	if err != nil {
		writeChatHistoryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeChatFeedback(w, http.StatusOK, "Chat thread retrieved successfully.", "", result)
}
