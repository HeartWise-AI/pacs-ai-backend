package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
)

// OrthancCommandServiceInterface holds the implementable methods for the Orthanc command service
type OrthancCommandServiceInterface interface {
	RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint) (orthancAPITypes.RetrieveQueryModalityAnswerResponse, error)
}
