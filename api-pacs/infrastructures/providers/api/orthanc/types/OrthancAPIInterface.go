package types

import (
	"context"
)

// OrthancAPIInterface list of implementable methods for orthanc api
type OrthancAPIInterface interface {
	DeleteLocalResources(ctx context.Context, requestPayload DeleteLocalResourcesRequest) error
	FindLocalResources(ctx context.Context) ([]GetLocalResourceResponse, error)
	FindLocalStudy(ctx context.Context, requestPayload QueryLocalStudyRequest) ([]string, error)
	FindModalityStudies(ctx context.Context, aet string, requestPayload QueryModalitiesRequest) ([]QueryModalitiesAnswersResponse, string, error)
	GetJobInfo(ctx context.Context, jobID string) (GetJobResponse, error)
	RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint, requestPayload RetrieveQueryModalityAnswerRequest) (RetrieveQueryModalityAnswerResponse, error)
}
