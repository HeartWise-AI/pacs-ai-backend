package service

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	apiError "api-pacs/internal/errors"
	hashUtils "api-pacs/internal/hash"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/orthanc/domain/repository"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// OrthancCommandService handles the Orthanc command service logic
type OrthancCommandService struct {
	repository.OrthancCommandRepositoryInterface
	repository.OrthancQueryRepositoryInterface
	repository.OrthancTenantStudyRepositoryInterface
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	userApplication.UserQueryServiceInterface
	// StoreBackReports looks up the model-run report a store-back may send.
	// Nil refuses every store-back.
	StoreBackReports StoreBackReportSource
}

const (
	customSeriesInstanceUIDHashFormat string = "%s:%s:%s" // <tenant_id>:<series_instance_uids_asc_order>:<model_name>_<model_version>
	customSOPInstanceUIDHashFormat    string = "%s:%s"    // <tenant_id>:<derived_series_instance_uid>
)

// modalityWrites serializes writes to Orthanc's modality table so that
// RestrictDICOMModalityRights never rewrites a modality from a stale listing.
var modalityWrites sync.Mutex

// inboundDICOMModality is what a remote PACS may do on Orthanc's DICOM port:
// C-STORE studies into it and C-ECHO it. C-FIND and C-MOVE that Orthanc
// issues to the PACS do not depend on these flags.
func inboundDICOMModality(aet, host string, port uint, useDicomTLS bool) orthancAPITypes.UpdateDICOMModalityRequest {
	return orthancAPITypes.UpdateDICOMModalityRequest{
		AET:                    aet,
		AllowEcho:              true,
		AllowFind:              false,
		AllowFindWorklist:      false,
		AllowGet:               false,
		AllowMove:              false,
		AllowStorageCommitment: false,
		AllowStore:             true,
		AllowTranscoding:       true,
		Host:                   host,
		Port:                   port,
		UseDicomTLS:            useDicomTLS,
	}
}

// ClearLocalStudiesCache clear local studies cache
func (service *OrthancCommandService) ClearLocalStudiesCache(ctx context.Context) error {
	// get all local studies
	localResources, err := service.OrthancAPIInterface.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level:  "Study",
		Expand: true,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	var expiredResources []string

	// get local cache expiration config
	localCacheExpiration, err := strconv.Atoi(os.Getenv("ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS"))
	if err != nil || localCacheExpiration <= 0 {
		// override to 24h if invalid or <=0
		log.Println("[Cache] invalid cache expiration config, using default 24h")
		localCacheExpiration = 24
	}

	for _, resource := range localResources {
		lastUpdateTime, err := time.Parse("20060102T150405", resource.LastUpdate)
		if err != nil {
			log.Println(err)
			return err
		}

		// check if last update time is more than local cache expiration
		expirationTime := lastUpdateTime.Add(time.Hour * time.Duration(localCacheExpiration))

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

// RestrictDICOMModalityRights rewrites every modality registered in Orthanc
// that still allows more than inbound C-STORE and C-ECHO, such as those saved
// before UpdateDICOMModality narrowed its rights.
func (service *OrthancCommandService) RestrictDICOMModalityRights(ctx context.Context) error {
	modalityWrites.Lock()
	defer modalityWrites.Unlock()

	modalities, err := service.OrthancAPIInterface.ListDICOMModalities(ctx)
	if err != nil {
		return err
	}

	for modalityID, modality := range modalities {
		if !modality.AllowFind && !modality.AllowFindWorklist && !modality.AllowGet && !modality.AllowMove &&
			!modality.AllowNAction && !modality.AllowEventReport {
			continue
		}

		err := service.OrthancAPIInterface.UpdateDICOMModality(ctx, modalityID, inboundDICOMModality(modality.AET, modality.Host, modality.Port, modality.UseDicomTLS))
		if err != nil {
			return err
		}
		log.Println("[Modalities] restricted to inbound C-STORE and C-ECHO:", modalityID)
	}

	return nil
}

// RemoveDICOMModality remove dicom modality
func (service *OrthancCommandService) RemoveDICOMModality(ctx context.Context, tenantID string, modalityID string) error {
	dicomModality, err := authorizeDICOMModality(ctx, service.OrthancCommandRepositoryInterface, tenantID, modalityID)
	if err != nil {
		return err
	}

	modalityWrites.Lock()
	err = service.OrthancAPIInterface.DeleteDICOMModality(ctx, dicomModality.ModalityID)
	modalityWrites.Unlock()
	if err != nil {
		log.Println(err)
		return err
	}

	// delete dicom modality in database
	err = service.OrthancCommandRepositoryInterface.DeleteDICOMModality(ctx, dicomModality.TenantID, dicomModality.ModalityID)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// RetrieveModalityStudyBySeries retrieve modality study by series
func (service *OrthancCommandService) RetrieveModalityStudyBySeries(ctx context.Context, data types.RetrieveModalityStudyBySeries) ([]orthancAPITypes.QueryModalityResponse, error) {
	// Only what the local store lacks is moved: series that are not stored,
	// and the missing images of partly stored ones. Nothing missing answers
	// DuplicateRecord, as an up-to-date local copy did before.
	// Every modality, ultrasound included, is retrieved series by series:
	// one C-MOVE per series, first series first, a few at a time. Ultrasound
	// used one C-MOVE per image, which opened up to one association per image
	// against the PACS and got refused under load.
	// Someone waiting in the viewer (a user ID) goes before automatic ingestion.
	dicomModality, err := authorizeDICOMModality(ctx, service.OrthancCommandRepositoryInterface, data.TenantID, data.ModalityID)
	if err != nil {
		return nil, err
	}
	data.ModalityID = dicomModality.ModalityID

	res, err := service.OrthancAPIInterface.RetrieveModalityStudyBySeries(ctx, data.ModalityID, os.Getenv("ORTHANC_AET"), data.StudyInstanceUID, data.UserID != nil)
	if err != nil && err.Error() != apiError.DuplicateRecord {
		log.Println(err)
		return nil, err
	}

	// The modality has the study. Retrieved through a modality the tenant
	// moves from, the tenant may read it from the shared Orthanc.
	if dicomModality.CMoveEnabled && service.OrthancTenantStudyRepositoryInterface != nil {
		if recordErr := service.OrthancTenantStudyRepositoryInterface.InsertTenantStudy(ctx, data.TenantID, data.StudyInstanceUID); recordErr != nil {
			return nil, errors.New(apiError.DatabaseError)
		}
	}
	if err != nil {
		return nil, err // halt and proceed: the local copy is complete
	}

	// logs to elasticsearch
	if data.UserID != nil {
		go func() {
			user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, *data.UserID)
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
				ModalityID:       data.ModalityID,
				UserID:           *data.UserID,
				Email:            user.Email,
				Name:             user.Name,
				StudyInstanceUID: data.StudyInstanceUID,
			})
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	return res, nil
}

// TriggerDICOMEchoSCU trigger dicom echo scu
func (service *OrthancCommandService) TriggerDICOMEchoSCU(ctx context.Context, tenantID, modalityID string) error {
	dicomModality, err := authorizeDICOMModality(ctx, service.OrthancCommandRepositoryInterface, tenantID, modalityID)
	if err != nil {
		return err
	}

	err = service.OrthancAPIInterface.TriggerDICOMEchoSCU(ctx, dicomModality.ModalityID)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// UpdateDICOMModality update dicom modality
func (service *OrthancCommandService) UpdateDICOMModality(ctx context.Context, data types.UpdateDICOMModality) error {
	dicomModality, err := authorizeDICOMModality(ctx, service.OrthancCommandRepositoryInterface, data.TenantID, data.ModalityID)
	if err != nil {
		return err
	}
	data.TenantID = dicomModality.TenantID
	data.ModalityID = dicomModality.ModalityID

	modalityWrites.Lock()
	err = service.OrthancAPIInterface.UpdateDICOMModality(ctx, data.ModalityID, inboundDICOMModality(data.AET, data.Host, data.Port, data.UseDicomTLS))
	modalityWrites.Unlock()
	if err != nil {
		log.Println(err)
		return err
	}

	// perform upsert
	err = service.OrthancCommandRepositoryInterface.UpsertDICOMModality(ctx, repositoryTypes.UpsertDICOMModality{
		TenantID:      data.TenantID,
		ModalityID:    data.ModalityID,
		AET:           data.AET,
		HostHash:      hashUtils.GetMD5Hash(data.Host), // hash host using md5
		CFindEnabled:  data.CFindEnabled,
		CMoveEnabled:  data.CMoveEnabled,
		CStoreEnabled: data.CStoreEnabled,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}
