package service

import (
	"context"
	"log"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/orthanc/domain/repository"
	"api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// OrthancQueryService handles the Orthanc query service logic
type OrthancQueryService struct {
	repository.OrthancQueryRepositoryInterface
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	userApplication.UserQueryServiceInterface
}

// FindLocalSOPInstance find local SOP instance
func (service *OrthancQueryService) FindLocalSOPInstance(ctx context.Context, sopInstanceUID string) ([]string, error) {
	queryIDs, err := service.OrthancAPIInterface.FindLocalSOPInstance(ctx, sopInstanceUID)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return queryIDs, nil
}

// FindModalityStudies get modality studies
func (service *OrthancQueryService) FindModalityStudies(ctx context.Context, data types.FindModalityStudies) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error) {
	res, queryID, err := service.OrthancAPIInterface.FindModalityStudies(ctx, data.ModalityID, orthancAPITypes.QueryModalitiesRequest{
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
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println(err)
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
			ModalityID: data.ModalityID,
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
	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	var results []orthancAPITypes.GetJobResponse

	// set limit
	eg.SetLimit(len(jobIDs))

	for _, jobID := range jobIDs {
		func(jobID string) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				// get job info
				job, err := service.OrthancAPIInterface.GetJobInfo(egCtx, jobID)
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
		log.Println(err)
		return nil, err
	}

	return results, nil
}

// ListDICOMModalities list dicom modalities
func (service *OrthancQueryService) ListDICOMModalities(ctx context.Context, tenantID string, modalityID *string) (map[string]types.ListDICOMModality, error) {
	orthancDicom, err := service.OrthancAPIInterface.ListDICOMModalities(ctx)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// get dicom modality from firestore by tenantID and modalityID
	firestoreDicom, err := service.OrthancQueryRepositoryInterface.SelectDICOMModalityByTenantModality(ctx, tenantID, *modalityID)
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println(err)
		return nil, err
	}

	result := make(map[string]types.ListDICOMModality)

	for key, orthancModality := range orthancDicom {
		combined := types.ListDICOMModality{
			AET:                 orthancModality.AET,
			AllowEcho:           orthancModality.AllowEcho,
			AllowFind:           orthancModality.AllowFind,
			AllowFindWorklist:   orthancModality.AllowFindWorklist,
			AllowGet:            orthancModality.AllowGet,
			AllowMove:           orthancModality.AllowMove,
			AllowStore:          orthancModality.AllowStore,
			AllowTranscoding:    orthancModality.AllowTranscoding,
			Host:                orthancModality.Host,
			Port:                orthancModality.Port,
			Timeout:             orthancModality.Timeout,
			UseDicomTLS:         orthancModality.UseDicomTLS,
			TargetCFindEnabled:  firestoreDicom.CFindEnabled,
			TargetCMoveEnabled:  firestoreDicom.CMoveEnabled,
			TargetCStoreEnabled: firestoreDicom.CStoreEnabled,
		}
		result[key] = combined
	}

	return result, nil
}
