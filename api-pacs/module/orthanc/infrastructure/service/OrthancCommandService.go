package service

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

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

// ClearLocalStudiesCache clear local studies cache
func (service *OrthancCommandService) ClearLocalStudiesCache(ctx context.Context) error {
	// get all local studies
	localResources, err := service.OrthancAPIInterface.FindLocalStudies(ctx)
	if err != nil {
		log.Println(err)
		return err
	}

	var expiredResources []string

	for _, resource := range localResources {
		lastUpdateTime, err := time.Parse("20060102T150405", resource.LastUpdate)
		if err != nil {
			log.Println(err)
			return err
		}

		// check if last update time is more than 24h
		expirationTime := lastUpdateTime.Add(time.Hour * 24)

		// if true, include the resource for bulk delete
		if time.Now().After(expirationTime) {
			expiredResources = append(expiredResources, resource.ID)
		}
	}

	if len(expiredResources) > 0 {
		err := service.OrthancAPIInterface.DeleteLocalResources(ctx, orthancAPITypes.DeleteLocalResourcesRequest{
			Resources: expiredResources,
		})
		if err != nil {
			log.Println(err)
			return err
		}

		log.Println("[Cache] deleted resources:", expiredResources)
	}

	return nil
}

// RetrieveModalityStudyBySeries retrieve modality study by series
func (service *OrthancCommandService) RetrieveModalityStudyBySeries(ctx context.Context, data types.RetrieveModalityStudyBySeries) ([]orthancAPITypes.QueryModalityResponse, error) {
	// check if study already exist in local
	studies, err := service.OrthancAPIInterface.FindLocalResource(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: "Study",
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: data.StudyInstanceUID,
		},
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println(err)
		return nil, err
	}

	// if existing, skip retrieving/download
	if len(studies) > 0 {
		return nil, errors.New(apiError.DuplicateRecord) // halt and proceed
	}

	res, err := service.OrthancAPIInterface.RetrieveModalityStudyBySeries(ctx, data.AET, orthancAPITypes.RetrieveModalityStudyBySeriesRequest{
		Level:     "Series",
		LocalAet:  os.Getenv("ORTHANC_AET"),
		Normalize: true,
		Query: orthancAPITypes.QueryModalitySeries{
			StudyInstanceUID: data.StudyInstanceUID,
		},
		Timeout: 0,
	})
	if err != nil {
		log.Println(err)
		return nil, err
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
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	return res, nil
}
