package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancCommandServiceInterface holds the implementable methods for the Orthanc command service
type OrthancCommandServiceInterface interface {
	ClearLocalStudiesCache(ctx context.Context) error
	RetrieveModalityStudyBySeries(ctx context.Context, data types.RetrieveModalityStudyBySeries) ([]orthancAPITypes.QueryModalityResponse, error)
}
