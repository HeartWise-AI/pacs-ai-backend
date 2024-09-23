package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancQueryServiceInterface holds the implementable methods for the Orthanc query service
type OrthancQueryServiceInterface interface {
	FindLocalSOPInstance(ctx context.Context, sopInstanceUID string) ([]string, error)
	FindModalityStudies(ctx context.Context, data types.FindModalityStudies) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error)
	GetJobsInfo(ctx context.Context, jobIDs []string) ([]orthancAPITypes.GetJobResponse, error)
	ListDICOMModalities(ctx context.Context) (map[string]orthancAPITypes.ListDICOMModalitiesResponse, error)
}
