package types

import (
	"context"
)

// OrthancAPIInterface list of implementable methods for orthanc api
type OrthancAPIInterface interface {
	GetModalityStudies(ctx context.Context, aet string, requestPayload QueryModalitiesRequest) ([]QueryModalitiesAnswersResponse, error)
}
