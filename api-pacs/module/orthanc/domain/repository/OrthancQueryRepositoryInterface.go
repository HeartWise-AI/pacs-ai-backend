package repository

import (
	"context"

	"api-pacs/module/orthanc/domain/entity"
)

// OrthancQueryRepositoryInterface is the interface for orthanc query repository
type OrthancQueryRepositoryInterface interface {
	// SelectDICOMModalityByModalityID get DICOM modality by modality id
	SelectDICOMModalityByModalityID(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error)
}
