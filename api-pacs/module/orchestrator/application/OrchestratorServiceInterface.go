package application

import (
	"context"

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
}
