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
	"api-pacs/module/orthanc/domain/entity"
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
	if data.UserID != nil {
		go func() {
			user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, *data.UserID)
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
				UserID:     *data.UserID,
				Email:      user.Email,
				Name:       user.Name,
				QueryID:    queryID,
			})
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

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

// GetLinkedDICOMModalityWithEnabledCStore get linked DICOM modality with enabled C-Store
func (service *OrthancQueryService) GetLinkedDICOMModalityWithEnabledCStore(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	// get host hash
	dicomModality, err := service.OrthancQueryRepositoryInterface.SelectDICOMModalityByModalityID(ctx, tenantID, modalityID)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, err
	}

	// get linked dicom modality with c-store enabled
	linkedDICOMModality, err := service.OrthancQueryRepositoryInterface.SelectLinkedDICOMModalityWithEnabledCStore(ctx, tenantID, dicomModality.HostHash)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, err
	}

	return linkedDICOMModality, nil
}

// ListDICOMModalities list dicom modalities
func (service *OrthancQueryService) ListDICOMModalities(ctx context.Context, tenantID string) (map[string]types.ListDICOMModalityResult, error) {
	res, err := service.OrthancAPIInterface.ListDICOMModalities(ctx)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	var m = sync.Mutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	// set limit
	eg.SetLimit(len(res))

	results := map[string]types.ListDICOMModalityResult{}

	for dicomModalityID, dicomModality := range res {
		func(dicomModalityID string, dicomModality orthancAPITypes.ListDICOMModalitiesResponse) {
			eg.Go(func() error {
				m.Lock()
				defer m.Unlock()

				// get dicom modality from database
				dbDicomModality, err := service.OrthancQueryRepositoryInterface.SelectDICOMModalityByModalityID(egCtx, tenantID, dicomModalityID)
				if err != nil {
					log.Println(err) // silently ignore error
				}

				results[dicomModalityID] = types.ListDICOMModalityResult{
					AET:                 dicomModality.AET,
					AllowEcho:           dicomModality.AllowEcho,
					AllowFind:           dicomModality.AllowFind,
					AllowFindWorklist:   dicomModality.AllowFindWorklist,
					AllowGet:            dicomModality.AllowGet,
					AllowMove:           dicomModality.AllowMove,
					AllowStore:          dicomModality.AllowStore,
					AllowTranscoding:    dicomModality.AllowTranscoding,
					Host:                dicomModality.Host,
					Port:                dicomModality.Port,
					Timeout:             dicomModality.Timeout,
					UseDicomTLS:         dicomModality.UseDicomTLS,
					TargetCFindEnabled:  dbDicomModality.CFindEnabled,
					TargetCMoveEnabled:  dbDicomModality.CMoveEnabled,
					TargetCStoreEnabled: dbDicomModality.CStoreEnabled,
				}

				return nil
			})
		}(dicomModalityID, dicomModality)
	}

	// wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		log.Println(err)
		return nil, err
	}

	return results, nil
}
