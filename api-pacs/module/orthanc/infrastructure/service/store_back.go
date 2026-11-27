package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	dicomUtils "api-pacs/internal/dicom"
	apiError "api-pacs/internal/errors"
	hashUtils "api-pacs/internal/hash"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// StoreBackReportSource returns the report registered for one of the tenant's
// completed model runs: the patient and study it belongs to, the model, and
// the PDF to send. executionID is opaque to this package.
type StoreBackReportSource interface {
	RegisteredStoreBackReport(ctx context.Context, tenantID, executionID string) (patientID, patientName, studyInstanceUID, modelName, modelVersion string, pdf []byte, seriesInstanceUIDs []string, err error)
}

// StoreStudyCustomSeries C-STOREs a registered model-run report to one of the
// tenant's modalities. The modality is looked up once and must have C-STORE
// enabled; the DICOM is built only from the registered report, so a caller
// cannot choose the patient, study, model or file that reaches the PACS.
func (service *OrthancCommandService) StoreStudyCustomSeries(ctx context.Context, data types.StoreStudyCustomSeries) error {
	executionID := strings.TrimSpace(data.ExecutionID)
	if strings.TrimSpace(data.TenantID) == "" || executionID == "" {
		return errors.New(apiError.InvalidPayload)
	}

	dicomModality, err := authorizeDICOMModality(ctx, service.OrthancCommandRepositoryInterface, data.TenantID, data.ModalityID)
	if err != nil {
		return err
	}
	if !dicomModality.CStoreEnabled {
		return errors.New(apiError.ForbiddenAccess)
	}
	modalityID := dicomModality.ModalityID

	if service.StoreBackReports == nil {
		return errors.New(apiError.MissingRecord)
	}
	patientID, patientName, studyInstanceUID, modelName, modelVersion, pdf, seriesInstanceUIDs, err := service.StoreBackReports.RegisteredStoreBackReport(ctx, data.TenantID, executionID)
	if err != nil {
		log.Printf("[store-back] no registered report tenant_id=%s err=%v", data.TenantID, err)
		return errors.New(apiError.MissingRecord)
	}
	if patientID == "" || studyInstanceUID == "" || modelName == "" || modelVersion == "" || !bytes.HasPrefix(pdf, []byte("%PDF")) {
		return errors.New(apiError.MissingRecord)
	}
	hashSeries := seriesInstanceUIDs
	if len(hashSeries) == 0 {
		hashSeries = []string{executionID}
	}

	// 1.2.826.0.1.3680043.10.511. + crc32 of <tenant>:<source series>:<model>_<version>
	uniqueSeriesID := hashUtils.GetCRC32DigitHash(fmt.Sprintf(customSeriesInstanceUIDHashFormat, data.TenantID, strings.Join(hashSeries, ":"), strings.ToLower(modelName+"_"+modelVersion)))
	customSeriesInstanceUID := fmt.Sprintf("1.2.826.0.1.3680043.10.511.%s", uniqueSeriesID)
	uniqueInstanceID := hashUtils.GetCRC32DigitHash(fmt.Sprintf(customSOPInstanceUIDHashFormat, data.TenantID, customSeriesInstanceUID))
	customSOPInstanceUID := fmt.Sprintf("1.2.826.0.1.3680043.10.511.%s", uniqueInstanceID)
	seriesDescription := fmt.Sprintf("%s-%s Report", modelName, modelVersion)

	dicomBytes, err := dicomUtils.ConvertPDFToDICOM(pdf, studyInstanceUID, customSeriesInstanceUID, customSOPInstanceUID, seriesDescription, patientID, patientName)
	if err != nil {
		log.Println("[dicom] error converting pdf to dicom:", err)
		return errors.New(apiError.DICOMParseError)
	}

	uploaded, err := service.OrthancAPIInterface.UploadDICOMInstances(ctx, dicomBytes)
	if err != nil {
		log.Println("[orthanc] error uploading DICOM instances:", err)
		return errors.New(apiError.OrthancError)
	}
	if uploaded.Status == orthancAPITypes.UploadDICOMStatusAlreadyStored {
		return errors.New(apiError.DuplicateRecord)
	}
	if uploaded.Status != orthancAPITypes.UploadDICOMStatusSuccess {
		log.Println("[orthanc] error uploading DICOM instances:", uploaded.Status)
		return errors.New(apiError.OrthancError)
	}

	storeRes, err := service.OrthancAPIInterface.StraightDICOMStoreSCU(ctx, modalityID, dicomBytes)
	if err != nil {
		log.Println("[orthanc] error straight DICOM store SCU:", err)
		if deleteErr := service.OrthancAPIInterface.DeleteLocalResources(ctx, orthancAPITypes.DeleteLocalResourcesRequest{
			Resources: []string{uploaded.ID},
		}); deleteErr != nil {
			log.Println("[orthanc] error deleting local resource:", deleteErr)
		}
		return errors.New(apiError.OrthancError)
	}
	log.Printf("[store-back] stored tenant_id=%s modality_id=%s sop_class=%s", data.TenantID, modalityID, storeRes.SOPClassUID)

	go func() {
		auditContext := context.WithoutCancel(ctx)
		if service.UserQueryServiceInterface == nil || service.TenantQueryServiceInterface == nil || service.ElasticsearchCommandServiceInterface == nil {
			return
		}
		user, err := service.UserQueryServiceInterface.GetTenantUserByID(auditContext, data.TenantID, data.UserID)
		if err != nil {
			return
		}
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(auditContext, data.TenantID)
		if err != nil {
			return
		}
		if _, err := service.ElasticsearchCommandServiceInterface.CreateStoredCustomSeriesLog(auditContext, elasticsearchTypes.CreateStoredCustomSeriesLog{
			TenantID:                data.TenantID,
			TenantName:              tenant.Name,
			ModalityID:              modalityID,
			UserID:                  data.UserID,
			Email:                   user.Email,
			Name:                    user.Name,
			StudyInstanceUID:        studyInstanceUID,
			SeriesInstanceUIDs:      seriesInstanceUIDs,
			PatientID:               patientID,
			ModelName:               modelName,
			ModelVersion:            modelVersion,
			CustomSeriesInstanceUID: customSeriesInstanceUID,
			CustomSOPInstanceUID:    customSOPInstanceUID,
		}); err != nil {
			log.Println(err)
		}
	}()

	return nil
}
