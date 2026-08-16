package application

import (
	"context"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// ProcessingResultProviderInterface isolates user-triggered result retrieval
// from routine dispatch and reconciliation job reads.
type ProcessingResultProviderInterface interface {
	GetJobResultByID(ctx context.Context, tenantID, jobID string) (serviceTypes.StudyServiceJobResult, bool, error)
}
