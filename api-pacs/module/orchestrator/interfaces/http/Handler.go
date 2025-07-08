package http

import (
	"github.com/go-playground/validator/v10"

	"api-pacs/module/orchestrator/domain/entity"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"CreateMessageRequest.Message":              "Message is required.",
		"UploadDicomPayloadRequest.Payload":         "Payload is required.",
		"GetThreadRequest.ThreadID":                 "Thread ID is required.",
	}
)

// CreateMessageRequest represents a request to create a message
type CreateMessageRequest struct {
	Message    string                 `json:"message,omitempty"`
	ThreadID   string                 `json:"thread_id,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	ImageData  string                 `json:"image_data,omitempty"`
	ImageType  string                 `json:"image_type,omitempty"`
}

// StudyData represents a DICOM study data structure
type StudyData struct {
	StudyInstanceUID   string                 `json:"studyInstanceUID"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs"`
	Modality           *string                `json:"modality,omitempty"`
	PreviewImageBase64 *string                `json:"previewImageBase64,omitempty"`
}

// UploadDicomPayloadRequest represents a request to upload a DICOM payload
type UploadDicomPayloadRequest struct {
	Payload            []StudyData       `json:"payload" validate:"required"`
	ThreadID           string            `json:"thread_id,omitempty"`
	OutputMode         entity.OutputMode `json:"outputMode,omitempty"`
}

// GetThreadRequest represents a request to get thread information
type GetThreadRequest struct {
	ThreadID string `json:"thread_id" validate:"required"`
}
