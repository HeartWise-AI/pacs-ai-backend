package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancQueryServiceInterface holds the implementable methods for the Orthanc query service
type OrthancQueryServiceInterface interface {
	GetJobInfo(ctx context.Context, jobID string) (orthancAPITypes.GetJobResponse, error)
	GetModalityStudies(ctx context.Context, data types.GetModalityStudies) ([]orthancAPITypes.QueryModalitiesAnswersResponse, string, error)
}
