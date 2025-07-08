package types

import (
	"time"
)

// CreateThreadRequest represents a request to create a thread
type CreateThreadRequest struct {
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// CreateThreadResponse represents a response from creating a thread
type CreateThreadResponse struct {
	ThreadID string `json:"thread_id"`
}

// CreateMessageRequest represents a request to create a message
type CreateMessageRequest struct {
	ThreadID   string                 `json:"thread_id"`
	Content    string                 `json:"content"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	ImageData  string                 `json:"image_data,omitempty"`
	ImageType  string                 `json:"image_type,omitempty"`
}

// CreateMessageResponse represents a response from creating a message
type CreateMessageResponse struct {
	ThreadID    string                 `json:"thread_id"`
	Message     *MessageResponse       `json:"message,omitempty"`
	Status      string                 `json:"status"`
	Error       string                 `json:"error,omitempty"`
	MessageID   string                 `json:"message_id,omitempty"`
	Content     string                 `json:"content,omitempty"`
	Response    string                 `json:"response,omitempty"`
	ResponseID  string                 `json:"response_id,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at,omitempty"`
	CompletedAt time.Time              `json:"completed_at,omitempty"`
}

// GetThreadResponse represents a response from getting thread information
type GetThreadResponse struct {
	ID              string                 `json:"id"`
	ThreadID        string                 `json:"thread_id"`
	Messages        []MessageResponse      `json:"messages"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	HasDicomPayload bool                   `json:"has_dicom_payload"`
	Status          string                 `json:"status,omitempty"`
	Error           string                 `json:"error,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// ToolResultResponse represents a tool result in a message response
type ToolResultResponse struct {
	ToolName string      `json:"tool_name"`
	Result   interface{} `json:"result"`
}

// MessageResponse represents a message in a thread response
type MessageResponse struct {
	Role        string               `json:"role"` // 'assistant' or 'tool'
	Content     string               `json:"content"`
	ToolResults []ToolResultResponse `json:"tool_results,omitempty"`
	// Keep these fields for backward compatibility
	ID        string                 `json:"id,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"createdAt,omitempty"`
}

// ChatResponse represents a response from a chat message
type ChatResponse struct {
	ThreadID string           `json:"thread_id"`
	Message  *MessageResponse `json:"message,omitempty"`
	Status   string           `json:"status" default:"success"`
	Error    string           `json:"error,omitempty"`
}

// MessageRequest represents a request to send a message
type MessageRequest struct {
	Message  string `json:"message,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

// StudyData represents a DICOM study data structure
type StudyData struct {
	StudyInstanceUID   string                 `json:"studyInstanceUID"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs"`
	Modality           *string                `json:"modality,omitempty"`
	PreviewImageBase64 *string                `json:"previewImageBase64,omitempty"`
}

type DicomPayloadRequest struct {
	Payload     []StudyData `json:"payload"`
	ThreadID    *string     `json:"thread_id,omitempty"`
	BearerToken *string     `json:"bearer_token,omitempty"`
	APIBaseURL  *string     `json:"api_base_url,omitempty"`
}

// UploadDicomPayloadResponse represents a response from uploading a DICOM payload
type UploadDicomPayloadResponse struct {
	ThreadID string `json:"thread_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Success  bool   `json:"success"`
}
