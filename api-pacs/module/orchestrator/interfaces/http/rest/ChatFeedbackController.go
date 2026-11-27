package rest

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	orchestratorService "api-pacs/module/orchestrator/infrastructure/service"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

// Chat feedback (PACS-448). Votes: PUT/DELETE
// /v1/orchestrator/threads/{threadID}/messages/{messageID}/feedback (feedback.vote).
// Admin panel: GET /v1/inference/chat/feedback/list, /counts and /{feedbackID}
// (feedback.view); list and one vote carry the voter's name and email.

const (
	chatFeedbackMaxBodyBytes    = 16 << 10
	chatFeedbackMaxCategoryLen  = 40
	chatFeedbackMaxCommentChars = 5000
)

type chatFeedbackBody struct {
	Vote     string  `json:"vote"`
	Category string  `json:"category"`
	Comment  *string `json:"comment"`
}

func writeChatFeedback(w http.ResponseWriter, status int, message string, code string, data interface{}) {
	response := viewmodels.HTTPResponseVM{
		Status:  status,
		Success: status < 300,
		Message: message,
		Data:    data,
	}
	if code != "" {
		response.ErrorCode = code
	}
	response.JSON(w)
}

func writeChatFeedbackError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orchestratorService.ErrChatFeedbackNotFound):
		writeChatFeedback(w, http.StatusNotFound, "Answer or vote not found.", err.Error(), nil)
	case errors.Is(err, orchestratorService.ErrChatFeedbackInvalid):
		writeChatFeedback(w, http.StatusBadRequest, "Invalid feedback.", err.Error(), nil)
	case errors.Is(err, orchestratorService.ErrChatFeedbackNotConfigured):
		writeChatFeedback(w, http.StatusServiceUnavailable, "Chat feedback is not configured.", err.Error(), nil)
	default:
		writeChatFeedback(w, http.StatusBadGateway, "Chat feedback is unavailable.", "chat_feedback_unavailable", nil)
	}
}

// chatFeedbackTarget reads and checks the thread and message ids of the route.
func chatFeedbackTarget(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	threadID := chi.URLParam(r, "threadID")
	messageID := chi.URLParam(r, "messageID")
	if !orchestratorThreadIDPattern.MatchString(threadID) || !orchestratorThreadIDPattern.MatchString(messageID) {
		writeChatFeedback(w, http.StatusBadRequest, "Thread or message ID is invalid.", apiError.InvalidRequestPayload, nil)
		return "", "", false
	}
	return threadID, messageID, true
}

// SubmitChatFeedback saves the caller's thumbs up/down on one chat answer.
func (controller *OrchestratorController) SubmitChatFeedback(w http.ResponseWriter, r *http.Request) {
	threadID, messageID, ok := chatFeedbackTarget(w, r)
	if !ok {
		return
	}
	var body chatFeedbackBody
	if err := requestbody.DecodeJSON(w, r, &body, chatFeedbackMaxBodyBytes); err != nil {
		writeOrchestratorDecodeError(w, r, err, "orchestrator_chat_feedback", chatFeedbackMaxBodyBytes)
		return
	}
	body.Category = strings.TrimSpace(body.Category)
	if (body.Vote != "up" && body.Vote != "down") || body.Category == "" || len(body.Category) > chatFeedbackMaxCategoryLen {
		writeChatFeedback(w, http.StatusBadRequest, "vote must be up or down, with a category.", apiError.InvalidRequestPayload, nil)
		return
	}
	if body.Comment != nil && utf8.RuneCountInString(*body.Comment) > chatFeedbackMaxCommentChars {
		writeChatFeedback(w, http.StatusBadRequest, "The comment is too long.", apiError.InvalidRequestPayload, nil)
		return
	}
	result, err := controller.OrchestratorServiceInterface.SubmitChatFeedback(r.Context(), threadID, messageID, types.ChatFeedbackVote{
		Vote:     body.Vote,
		Category: body.Category,
		Comment:  body.Comment,
		Client:   "web",
	})
	if err != nil {
		writeChatFeedbackError(w, err)
		return
	}
	writeChatFeedback(w, http.StatusOK, "Feedback saved.", "", result)
}

// RemoveChatFeedback removes the caller's vote on one chat answer.
func (controller *OrchestratorController) RemoveChatFeedback(w http.ResponseWriter, r *http.Request) {
	threadID, messageID, ok := chatFeedbackTarget(w, r)
	if !ok {
		return
	}
	if err := controller.OrchestratorServiceInterface.RemoveChatFeedback(r.Context(), threadID, messageID); err != nil {
		writeChatFeedbackError(w, err)
		return
	}
	writeChatFeedback(w, http.StatusOK, "Feedback removed.", "", map[string]bool{"removed": true})
}

func chatFeedbackTenant(r *http.Request) string {
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	return tenantID
}

// ListChatFeedback lists the workspace's votes on chat answers, thumbs down first.
func (controller *OrchestratorController) ListChatFeedback(w http.ResponseWriter, r *http.Request) {
	result, err := controller.OrchestratorServiceInterface.ListChatFeedback(r.Context(), r.URL.Query())
	if err != nil {
		writeChatFeedbackError(w, err)
		return
	}
	result = controller.nameChatFeedbackUsers(r.Context(), chatFeedbackTenant(r), result)
	w.Header().Set("Cache-Control", "no-store")
	writeChatFeedback(w, http.StatusOK, "Chat feedback retrieved successfully.", "", result)
}

// GetChatFeedback returns one vote with the tool calls behind the rated answer.
func (controller *OrchestratorController) GetChatFeedback(w http.ResponseWriter, r *http.Request) {
	feedbackID := chi.URLParam(r, "feedbackID")
	if !orchestratorThreadIDPattern.MatchString(feedbackID) {
		writeChatFeedback(w, http.StatusBadRequest, "Feedback ID is invalid.", apiError.InvalidRequestPayload, nil)
		return
	}
	result, err := controller.OrchestratorServiceInterface.GetChatFeedback(r.Context(), feedbackID)
	if err != nil {
		writeChatFeedbackError(w, err)
		return
	}
	result = controller.nameChatFeedbackUsers(r.Context(), chatFeedbackTenant(r), result)
	w.Header().Set("Cache-Control", "no-store")
	writeChatFeedback(w, http.StatusOK, "Chat feedback retrieved successfully.", "", result)
}

// CountChatFeedback counts the workspace's votes on chat answers.
func (controller *OrchestratorController) CountChatFeedback(w http.ResponseWriter, r *http.Request) {
	result, err := controller.OrchestratorServiceInterface.CountChatFeedback(r.Context(), r.URL.Query())
	if err != nil {
		writeChatFeedbackError(w, err)
		return
	}
	writeChatFeedback(w, http.StatusOK, "Chat feedback counts retrieved successfully.", "", result)
}
