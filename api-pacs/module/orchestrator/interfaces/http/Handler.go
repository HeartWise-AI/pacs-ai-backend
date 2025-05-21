package http

import (
	"github.com/go-playground/validator/v10"

	"api-pacs/module/orchestrator/domain/entity"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"CreateMessageRequest.Message":                 "Message is required.",
		"UploadDicomPayloadRequest.StudyInstanceUID":   "Study Instance UID is required.",
		"UploadDicomPayloadRequest.SeriesInstanceUIDs": "Series Instance UIDs are required.",
		"GetThreadRequest.ThreadID":                    "Thread ID is required.",
	}
)

// CreateMessageRequest represents a request to create a message
type CreateMessageRequest struct {
	Message  string                 `json:"message,omitempty"`
	ThreadID string                 `json:"thread_id,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// UploadDicomPayloadRequest represents a request to upload a DICOM payload
type UploadDicomPayloadRequest struct {
	StudyInstanceUID   string                 `json:"studyInstanceUID" validate:"required"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs" validate:"required"`
	ThreadID           string                 `json:"thread_id,omitempty"`
	OutputMode         entity.OutputMode      `json:"outputMode,omitempty"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata,omitempty"`
	ContainerID        string                 `json:"containerId,omitempty"`
}

// GetThreadRequest represents a request to get thread information
type GetThreadRequest struct {
	ThreadID string `json:"thread_id" validate:"required"`
}
