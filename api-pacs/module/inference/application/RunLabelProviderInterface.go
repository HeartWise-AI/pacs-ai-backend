package application

import (
	"context"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// RunLabelProviderInterface reads and changes the labels study-service keeps
// on model runs (one study-service job each) and the tenant's label catalog.
type RunLabelProviderInterface interface {
	// GetJobLabels returns found=false when the job is not the tenant's.
	GetJobLabels(ctx context.Context, tenantID, jobID string) (serviceTypes.StudyServiceJobLabels, bool, error)
	// AddJobLabel returns added=false when the run already had the label.
	AddJobLabel(ctx context.Context, tenantID, jobID, label, createdBy string) (labels serviceTypes.StudyServiceJobLabels, added bool, err error)
	RemoveJobLabel(ctx context.Context, tenantID, jobID, label string) (serviceTypes.StudyServiceJobLabels, error)
	GetRunLabelCatalog(ctx context.Context, tenantID string) (serviceTypes.RunLabelCatalog, error)
}
