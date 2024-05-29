package service

import (
	"context"
	"log"
	"os"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// OrthancQueryService handles the Orthanc query service logic
type OrthancQueryService struct {
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	userApplication.UserQueryServiceInterface
}

// GetJobInfo get job info
func (service *OrthancQueryService) GetJobInfo(ctx context.Context, jobID string) (orthancAPITypes.GetJobResponse, error) {
	res, err := service.OrthancAPIInterface.GetJobInfo(ctx, jobID)
	if err != nil {
		return orthancAPITypes.GetJobResponse{}, err
	}

	return res, nil
}

// GetModalityStudies get modality studies
func (service *OrthancQueryService) GetModalityStudies(ctx context.Context, data types.GetModalityStudies) ([]orthancAPITypes.QueryModalitiesAnswersResponse, string, error) {
	// get tenant info
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
	if err != nil {
		return nil, "", err
	}

	res, queryID, err := service.OrthancAPIInterface.GetModalityStudies(ctx, tenant.AET, orthancAPITypes.QueryModalitiesRequest{
		Level:     "Study",
		LocalAET:  os.Getenv("ORTHANC_AET"),
		Normalize: true,
		Query: orthancAPITypes.QueryStudy{
			AccessionNumber:            data.AccessionNumber,
			InstitutionName:            data.InstitutionName,
			ModalitiesInStudy:          data.ModalitiesInStudy,
			NumberOfStudyRelatedSeries: data.NumberOfStudyRelatedSeries,
			PatientBirthDate:           data.PatientBirthDate,
			PatientID:                  data.PatientID,
			PatientName:                data.PatientName,
			PatientSex:                 data.PatientSex,
			ReferringPhysicianName:     data.ReferringPhysicianName,
			RequestingPhysician:        data.RequestingPhysician,
			StudyDate:                  data.StudyDate,
			StudyDescription:           data.StudyDescription,
			StudyID:                    data.StudyID,
			StudyInstanceUID:           data.StudyInstanceUID,
			StudyTime:                  data.StudyTime,
		},
		Timeout: 0,
	})
	if err != nil {
		return nil, "", err
	}

	// logs to elasticsearch
	go func() {
		user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, data.UserID)
		if err != nil {
			return
		}

		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateGetModalityStudyLog(ctx, elasticsearchTypes.CreateGetModalityStudyLog{
			TenantID:   data.TenantID,
			TenantName: tenant.Name,
			TenantAET:  tenant.AET,
			UserID:     data.UserID,
			Email:      user.Email,
			Name:       user.Name,
			QueryID:    queryID,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	return res, queryID, nil
}
