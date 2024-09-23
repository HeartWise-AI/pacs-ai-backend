package types

import (
	"context"
)

// OrthancAPIInterface list of implementable methods for orthanc api
type OrthancAPIInterface interface {
	DeleteDICOMModality(ctx context.Context, modalityID string) error
	DeleteLocalResources(ctx context.Context, request DeleteLocalResourcesRequest) error
	DownloadDICOM(ctx context.Context, queryID string) ([]byte, error)
	FindLocalResources(ctx context.Context, request QueryLocalResourceRequest) ([]GetLocalResourceResponse, error)
	FindLocalSOPInstance(ctx context.Context, sopInstanceUID string) ([]string, error)
	FindModalitySeriesByStudy(ctx context.Context, modalityID, localAET, studyInstanceUID string) ([]QueryModalitySeriesAnswersResponse, error)
	FindModalityStudies(ctx context.Context, modalityID string, request QueryModalitiesRequest) ([]QueryModalityStudyAnswersResponse, string, error)
	GetJobInfo(ctx context.Context, jobID string) (GetJobResponse, error)
	ListDICOMModalities(ctx context.Context) (map[string]ListDICOMModalitiesResponse, error)
	RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint, request RetrieveQueryModalityAnswerRequest) (QueryModalityResponse, error)
	RetrieveModalityStudyBySeries(ctx context.Context, modalityID, localAet, studyInstanceUID string) ([]QueryModalityResponse, error)
	TriggerDICOMEchoSCU(ctx context.Context, modalityID string) error
	UpdateDICOMModality(ctx context.Context, modalityID string, request UpdateDICOMModalityRequest) error
}
