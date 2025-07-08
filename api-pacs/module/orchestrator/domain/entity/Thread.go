package entity

import "time"

// Thread represents a conversation thread in the orchestrator
type Thread struct {
	ID        string                 `json:"id"`
	Messages  []Message              `json:"messages"`
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`
}

// Message represents a message in a thread
type Message struct {
	ID        string                 `json:"id"`
	ThreadID  string                 `json:"threadId"`
	Content   string                 `json:"content"`
	Role      string                 `json:"role"` // "user" or "assistant"
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt time.Time              `json:"createdAt"`
}

// DicomPayload represents a DICOM payload attached to a thread
type DicomPayload struct {
	ThreadID           string                 `json:"threadId"`
	Payload            map[string]interface{} `json:"payload"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs"`
	StudyInstanceUID   string                 `json:"studyInstanceUID"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata"`
	CreatedAt          time.Time              `json:"createdAt"`
}

// OutputMode represents the output mode for the orchestrator
type OutputMode string

const (
	// OutputModeJSON is the JSON output mode
	OutputModeJSON OutputMode = "JSON"
	// OutputModeOHIFAnnotations is the OHIF annotations output mode
	OutputModeOHIFAnnotations OutputMode = "OHIF_ANNOTATIONS"
	// OutputModeHTML is the HTML output mode
	OutputModeHTML OutputMode = "HTML"
	// OutputModeWebApp is the web app output mode
	OutputModeWebApp OutputMode = "WEB_APP"
	// OutputModePDF is the PDF output mode
	OutputModePDF OutputMode = "PDF"
)
