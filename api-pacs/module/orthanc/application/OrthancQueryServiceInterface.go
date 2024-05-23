package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancQueryServiceInterface holds the implementable methods for the Orthanc query service
type OrthancQueryServiceInterface interface {
	GetModalityStudies(ctx context.Context, tenantID string, data types.GetModalityStudies) ([]orthancAPITypes.QueryModalitiesAnswersResponse, error)
}
