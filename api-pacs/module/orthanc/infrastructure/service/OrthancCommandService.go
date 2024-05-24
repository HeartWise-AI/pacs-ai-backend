package service

import (
	"context"
	"os"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	tenantApplication "api-pacs/module/tenant/application"
)

// OrthancCommandService handles the Orthanc command service logic
type OrthancCommandService struct {
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
}

// RetrieveModalityStudy retrieve modality study
func (service *OrthancCommandService) RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint) (orthancAPITypes.RetrieveQueryModalityAnswerResponse, error) {
	res, err := service.OrthancAPIInterface.RetrieveModalityStudy(ctx, queryID, answerIndex, orthancAPITypes.RetrieveQueryModalityAnswerRequest{
		Asynchronous: true,
		Full:         true,
		Permissive:   true,
		Priority:     0,
		Simplify:     true,
		Synchronous:  false,
		TargetAet:    os.Getenv("ORTHANC_AET"),
		Timeout:      0,
	})
	if err != nil {
		return orthancAPITypes.RetrieveQueryModalityAnswerResponse{}, err
	}

	return res, nil
}
