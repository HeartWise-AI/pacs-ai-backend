package service

import (
	"context"
	"errors"
	"log"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/domain/entity"
	"api-pacs/module/orthanc/domain/repository"
)

// tenantDICOMModality returns the tenant's record for a modality. Every tenant
// shares one Orthanc modality table, so a modality is usable only by a tenant
// holding its tenantID:modalityID record, and Orthanc must be called with the
// modality ID of the returned record.
func tenantDICOMModality(ctx context.Context, modalities repository.OrthancQueryRepositoryInterface, tenantID, modalityID string) (entity.DICOMModality, error) {
	dicomModality, err := modalities.SelectDICOMModalityByModalityID(ctx, tenantID, modalityID)
	if err != nil || dicomModality.TenantID != tenantID || dicomModality.ModalityID == "" {
		log.Println("[orthanc] DICOM modality is not registered for tenant:", tenantID, modalityID, err)
		return entity.DICOMModality{}, errors.New(apiError.ForbiddenAccess)
	}

	return dicomModality, nil
}
