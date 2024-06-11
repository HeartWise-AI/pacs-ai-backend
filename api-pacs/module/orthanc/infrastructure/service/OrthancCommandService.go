package service

import (
	"context"
	"errors"
	"log"
	"os"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// OrthancCommandService handles the Orthanc command service logic
type OrthancCommandService struct {
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	userApplication.UserQueryServiceInterface
}

// RetrieveModalityStudy retrieve modality study
func (service *OrthancCommandService) RetrieveModalityStudy(ctx context.Context, data types.RetrieveModalityStudy) (orthancAPITypes.RetrieveQueryModalityAnswerResponse, error) {
	// check if study already exist in local
	studies, err := service.OrthancAPIInterface.FindLocalStudy(ctx, orthancAPITypes.QueryLocalStudyRequest{
		Level: "Study",
		Query: orthancAPITypes.QueryLocalStudy{
			StudyInstanceUID: data.StudyInstanceUID,
		},
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println(err)
		return orthancAPITypes.RetrieveQueryModalityAnswerResponse{}, err
	}

	// if existing, skip retrieving/download
	if len(studies) > 0 {
		return orthancAPITypes.RetrieveQueryModalityAnswerResponse{}, errors.New(apiError.DuplicateRecord) // halt and proceed
	}

	res, err := service.OrthancAPIInterface.RetrieveModalityStudy(ctx, data.QueryID, data.AnswerIndex, orthancAPITypes.RetrieveQueryModalityAnswerRequest{
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
		log.Println(err)
		return orthancAPITypes.RetrieveQueryModalityAnswerResponse{}, err
	}

	// logs to elasticsearch
	go func() {
		user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, data.UserID)
		if err != nil {
			log.Println(err)
			return
		}

		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			log.Println(err)
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateRetrieveStudyLog(ctx, elasticsearchTypes.CreateRetrieveStudyLog{
			TenantID:         data.TenantID,
			TenantName:       tenant.Name,
			TenantAET:        tenant.AET,
			UserID:           data.UserID,
			Email:            user.Email,
			Name:             user.Name,
			StudyInstanceUID: data.StudyInstanceUID,
			QueryID:          data.QueryID,
			AnswerIndex:      data.AnswerIndex,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	return res, nil
}
