package repository

import (
	"context"

	"api-pacs/module/orthanc/domain/entity"
)

// OrthancQueryRepositoryInterface is the interface for orthanc query repository
type OrthancQueryRepositoryInterface interface {
	// SelectDICOMModalityByTenantModality get DICOM modality by tenant and modality
	SelectDICOMModalityByTenantModality(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error)
}
