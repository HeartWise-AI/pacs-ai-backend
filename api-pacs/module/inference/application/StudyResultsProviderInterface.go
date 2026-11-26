package application

import (
	"context"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// StudyResultsProviderInterface reads the latest completed study-service
// results for one study. It never enqueues processing.
type StudyResultsProviderInterface interface {
	// GetLatestStudyResults returns found=false when study-service does not
	// expose the endpoint (HTTP 404) so mixed-version deployments degrade.
	// A non-empty label keeps only runs carrying it (case-insensitive).
	GetLatestStudyResults(ctx context.Context, tenantID, studyInstanceUID, modality, label string) (serviceTypes.StudyServiceLatestResults, bool, error)
}
