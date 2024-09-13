package types

import (
	"context"
)

// OrthancAPIInterface list of implementable methods for orthanc api
type OrthancAPIInterface interface {
	DeleteLocalResources(ctx context.Context, request DeleteLocalResourcesRequest) error
	DownloadDICOM(ctx context.Context, queryID string) ([]byte, error)
	FindLocalStudies(ctx context.Context) ([]GetLocalStudyResponse, error)
	FindLocalResource(ctx context.Context, request QueryLocalResourceRequest) ([]string, error)
	FindModalityStudies(ctx context.Context, AET string, request QueryModalitiesRequest) ([]QueryModalityStudyAnswersResponse, string, error)
	GetJobInfo(ctx context.Context, jobID string) (GetJobResponse, error)
	RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint, request RetrieveQueryModalityAnswerRequest) (QueryModalityResponse, error)
	RetrieveModalityStudyBySeries(ctx context.Context, AET string, request RetrieveModalityStudyBySeriesRequest) ([]QueryModalityResponse, error)
}
