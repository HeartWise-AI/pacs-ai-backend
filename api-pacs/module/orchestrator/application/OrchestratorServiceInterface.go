package application

import (
	"context"
	"encoding/json"
	"net/url"

	"api-pacs/module/orchestrator/infrastructure/service/types"
)

// OrchestratorServiceInterface defines the interface for the orchestrator service
type OrchestratorServiceInterface interface {
	// CreateThread creates a new thread with authentication from context
	CreateThread(ctx context.Context, request types.CreateThreadRequest) (types.CreateThreadResponse, error)

	// CreateMessage creates a new message in a thread and gets a response with authentication from context
	CreateMessage(ctx context.Context, request types.CreateMessageRequest) (types.CreateMessageResponse, error)

	// UploadDicomPayload uploads a DICOM payload to a thread with authentication from context
	UploadDicomPayload(ctx context.Context, request types.DicomPayloadRequest) (any, error)

	// GetThread gets thread information with authentication from context
	GetThread(ctx context.Context, threadID string) (types.GetThreadResponse, error)

	// GetChatProgress gets the steps so far of the chat request in flight for a thread
	GetChatProgress(ctx context.Context, threadID string) (types.ChatProgressResponse, error)

	// SubmitChatFeedback creates or replaces the caller's thumbs up/down on one chat answer
	SubmitChatFeedback(ctx context.Context, threadID, messageID string, vote types.ChatFeedbackVote) (map[string]interface{}, error)

	// RemoveChatFeedback removes the caller's vote on one chat answer
	RemoveChatFeedback(ctx context.Context, threadID, messageID string) error

	// ListChatFeedback lists the workspace's votes on chat answers (admin panel, feedback.view)
	ListChatFeedback(ctx context.Context, query url.Values) (json.RawMessage, error)

	// ListChatThreads lists the signed-in user's chat threads about one study (chat history)
	ListChatThreads(ctx context.Context, studyInstanceUID string, limit int) (json.RawMessage, error)

	// GetChatThreadHistory returns one of the signed-in user's threads with its messages and votes
	GetChatThreadHistory(ctx context.Context, threadID string) (json.RawMessage, error)

	// GetChatSettings returns the chat backend's settings the viewer needs (default answer language)
	GetChatSettings(ctx context.Context) (types.ChatSettingsResponse, error)

	// CountChatFeedback counts the workspace's votes on chat answers (admin panel, feedback.view)
	CountChatFeedback(ctx context.Context, query url.Values) (json.RawMessage, error)
}
