package repository

import (
	"context"

	"api-pacs/module/inference/infrastructure/repository/types"
)

// WorklistNotificationAudienceRepositoryInterface reads who an automatic
// processing run should notify.
type WorklistNotificationAudienceRepositoryInterface interface {
	// SelectProcessingRunNotificationJobs returns the distinct ingestion jobs
	// whose candidates have executions in the tenant-scoped run.
	SelectProcessingRunNotificationJobs(ctx context.Context, tenantID, processingRunID string) ([]types.ProcessingRunNotificationJob, error)
}
