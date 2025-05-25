package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/internal/assert"
	dicomUtils "api-pacs/internal/dicom"
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
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	userApplication.UserQueryServiceInterface
}

const (
	customSeriesInstanceUIDHashFormat string = "%s:%s:%s" // <tenant_id>:<series_instance_uids_asc_order>:<model_name>_<model_version>
	customSOPInstanceUIDHashFormat    string = "%s:%s"    // <tenant_id>:<derived_series_instance_uid>
)

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

// RemoveDICOMModality remove dicom modality
func (service *OrthancCommandService) RemoveDICOMModality(ctx context.Context, tenantID string, modalityID string) error {
	err := service.OrthancAPIInterface.DeleteDICOMModality(ctx, modalityID)
	if err != nil {
		log.Println(err)
		return err
	}

	// delete dicom modality in database
	err = service.OrthancCommandRepositoryInterface.DeleteDICOMModality(ctx, tenantID, modalityID)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// RetrieveModalityStudyBySeries retrieve modality study by series
func (service *OrthancCommandService) RetrieveModalityStudyBySeries(ctx context.Context, data types.RetrieveModalityStudyBySeries) ([]orthancAPITypes.QueryModalityResponse, error) {
	// check if study already exist in local
	resources, err := service.OrthancAPIInterface.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: "Series",
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: data.StudyInstanceUID,
		},
		Expand: true,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Println(err)
		return nil, err
	}

	if len(resources) > 0 {
		// check if local resource series matches with modality series
		// FIXME: ideal is to check it using last update time but modality query doesnt return time related
		// FIXME: code will detect series changes but not study related changes like change name, etc.
		var localSeries []string
		for _, resource := range resources {
			localSeries = append(localSeries, resource.MainDICOMTags.SeriesInstanceUID)
		}

		modalitySeriesResponse, err := service.OrthancAPIInterface.FindModalitySeriesByStudy(ctx, data.ModalityID, os.Getenv("ORTHANC_AET"), data.StudyInstanceUID)
		if err != nil {
			log.Println(err)
			return nil, err
		}

		var modalitySeries []string
		for _, modality := range modalitySeriesResponse {
			modalitySeries = append(modalitySeries, modality.SeriesInstanceUID)
		}

		// if matches (meaning no changes), skip retrieving/download
		if assert.ElementsMatch(localSeries, modalitySeries) {
			return nil, errors.New(apiError.DuplicateRecord) // halt and proceed
		}
	}

	res, err := service.OrthancAPIInterface.RetrieveModalityStudyBySeries(ctx, data.ModalityID, os.Getenv("ORTHANC_AET"), data.StudyInstanceUID)
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
			ModalityID:       data.ModalityID,
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

// StoreStudyCustomSeries store study custom series
func (service *OrthancCommandService) StoreStudyCustomSeries(ctx context.Context, data types.StoreStudyCustomSeries) error {
	/// check mime type
	if data.FileMimeType == "application/pdf" {
		// convert pdf to dicom
		// form the series instance uids. It should already be in ascending order (timestamp)
		var orderedSeriesInstanceUIDsFormat string
		if len(data.SeriesInstanceUIDs) == 1 {
			orderedSeriesInstanceUIDsFormat = data.SeriesInstanceUIDs[0]
		} else {
			orderedSeriesInstanceUIDsFormat = strings.Join(data.SeriesInstanceUIDs, ":")
		}

		currentTimestamp := time.Now().Unix()

		// first: use standard prefix: 1.2.826.0.1.3680043.10.511.
		// second: append unix timestamp in seconds
		// third: crc32 digit hash of <tenant_id>:<orderedSeriesInstanceUIDsFormat>:<model_name>_<model_version>
		uniqueSeriesID := hashUtils.GetCRC32DigitHash(fmt.Sprintf(customSeriesInstanceUIDHashFormat, data.TenantID, orderedSeriesInstanceUIDsFormat, strings.ToLower(data.ModelName+"_"+data.ModelVersion)))
		customSeriesInstanceUID := fmt.Sprintf("1.2.826.0.1.3680043.10.511.%d.%s", currentTimestamp, uniqueSeriesID)

		// first: use standard prefix: 1.2.826.0.1.3680043.10.511.
		// second: append unix timestamp in seconds
		// third: crc32 digit hash of <tenant_id>:<custom_series_instance_uid>
		uniqueInstanceID := hashUtils.GetCRC32DigitHash(fmt.Sprintf(customSOPInstanceUIDHashFormat, data.TenantID, customSeriesInstanceUID))
		customSOPInstanceUID := fmt.Sprintf("1.2.826.0.1.3680043.10.511.%d.%s", currentTimestamp, uniqueInstanceID)

		// e.g: cathef-v1.0.0 Report
		seriesDescription := fmt.Sprintf("%s-%s Report", data.ModelName, data.ModelVersion)

		log.Println("customSeriesInstanceUIDHash:", customSeriesInstanceUID)
		log.Println("customSOPInstanceUID:", customSOPInstanceUID)
		log.Println("seriesDescription:", seriesDescription)

		dicomInstancesBytes, err := dicomUtils.ConvertPDFToDICOM(data.FileBody, data.StudyInstanceUID, customSeriesInstanceUID, customSOPInstanceUID, seriesDescription, data.PatientID, data.PatientName)
		if err != nil {
			log.Println("[dicom] error converting pdf to dicom:", err)
			return errors.New(apiError.DICOMParseError)
		}

		data.FileBody = dicomInstancesBytes
	}

	/// upload to local orthanc
	uploadDICOMInstancesResponse, err := service.OrthancAPIInterface.UploadDICOMInstances(ctx, data.FileBody)
	if err != nil {
		log.Println("[orthanc] error uploading DICOM instances:", err)
		return errors.New(apiError.OrthancError)
	}

	if uploadDICOMInstancesResponse.Status != orthancAPITypes.UploadDICOMStatusSuccess && uploadDICOMInstancesResponse.Status != orthancAPITypes.UploadDICOMStatusAlreadyStored {
		log.Println("[orthanc] error uploading DICOM instances:", uploadDICOMInstancesResponse.Status)
		return errors.New(apiError.OrthancError)
	}

	// if already exist, return duplicate error
	if uploadDICOMInstancesResponse.Status == orthancAPITypes.UploadDICOMStatusAlreadyStored {
		return errors.New(apiError.DuplicateRecord)
	}

	/// forward to target dicom modality
	storeRes, err := service.OrthancAPIInterface.StraightDICOMStoreSCU(ctx, data.ModalityID, data.FileBody)
	if err != nil {
		log.Println("[orthanc] error straight DICOM store SCU:", err)

		// delete already uploaded local resource
		err = service.OrthancAPIInterface.DeleteLocalResources(ctx, orthancAPITypes.DeleteLocalResourcesRequest{
			Resources: []string{uploadDICOMInstancesResponse.ID},
		})
		if err != nil {
			log.Println("[orthanc] error deleting local resource:", err)
		}

		return errors.New(apiError.OrthancError)
	}

	log.Println("store SOPClassUID:", storeRes.SOPClassUID)
	log.Println("store SOPInstanceUID:", storeRes.SOPInstanceUID)

	// TODO: elasticsearch logs

	return nil
}

// TriggerDICOMEchoSCU trigger dicom echo scu
func (service *OrthancCommandService) TriggerDICOMEchoSCU(ctx context.Context, modalityID string) error {
	err := service.OrthancAPIInterface.TriggerDICOMEchoSCU(ctx, modalityID)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// UpdateDICOMModality update dicom modality
func (service *OrthancCommandService) UpdateDICOMModality(ctx context.Context, data types.UpdateDICOMModality) error {
	err := service.OrthancAPIInterface.UpdateDICOMModality(ctx, data.ModalityID, orthancAPITypes.UpdateDICOMModalityRequest{
		AET:                    data.AET,
		AllowEcho:              true,
		AllowFind:              true,
		AllowFindWorklist:      true,
		AllowGet:               true,
		AllowMove:              true,
		AllowStorageCommitment: true,
		AllowStore:             true,
		AllowTranscoding:       true,
		Host:                   data.Host,
		Port:                   data.Port,
		UseDicomTLS:            data.UseDicomTLS,
	})
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
