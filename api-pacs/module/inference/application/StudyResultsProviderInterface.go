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
	GetLatestStudyResults(ctx context.Context, tenantID, studyInstanceUID, modality string) (serviceTypes.StudyServiceLatestResults, bool, error)
}
