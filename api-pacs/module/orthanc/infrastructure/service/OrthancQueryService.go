package service

import (
	"context"
	"log"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"

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

// FindLocalResource find local resource
func (service *OrthancQueryService) FindLocalResource(ctx context.Context, data types.FindLocalResource) ([]string, error) {
	queryIDs, err := service.OrthancAPIInterface.FindLocalResource(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: data.Level,
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: data.Query.StudyInstanceUID,
			SOPInstanceUID:   data.Query.SOPInstanceUID,
		},
	})
	if err != nil {
		return nil, err
	}

	return queryIDs, nil
}

// FindModalityStudies get modality studies
func (service *OrthancQueryService) FindModalityStudies(ctx context.Context, data types.FindModalityStudies) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error) {
	// get tenant info
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
	if err != nil {
		return nil, "", err
	}

	res, queryID, err := service.OrthancAPIInterface.FindModalityStudies(ctx, tenant.AET, orthancAPITypes.QueryModalitiesRequest{
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

// GetJobsInfo get jobs info
func (service *OrthancQueryService) GetJobsInfo(ctx context.Context, jobIDs []string) ([]orthancAPITypes.GetJobResponse, error) {
	var rw = sync.RWMutex{}
	eg, _ := errgroup.WithContext(ctx)

	var results []orthancAPITypes.GetJobResponse

	// set limit
	eg.SetLimit(len(jobIDs))

	for _, jobID := range jobIDs {
		rw.Lock()

		func(jobID string) {
			eg.Go(func() error {
				defer rw.Unlock()

				// get job info
				job, err := service.OrthancAPIInterface.GetJobInfo(ctx, jobID)
				if err != nil {
					return err
				}

				results = append(results, job)
				return nil
			})
		}(jobID)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}
