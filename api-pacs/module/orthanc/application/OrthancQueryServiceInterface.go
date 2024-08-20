package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancQueryServiceInterface holds the implementable methods for the Orthanc query service
type OrthancQueryServiceInterface interface {
	FindLocalResource(ctx context.Context, data types.FindLocalResource) ([]string, error)
	FindModalityStudies(ctx context.Context, data types.FindModalityStudies) ([]orthancAPITypes.QueryModalitiesAnswersResponse, string, error)
	GetJobInfo(ctx context.Context, jobID string) (orthancAPITypes.GetJobResponse, error)
}
