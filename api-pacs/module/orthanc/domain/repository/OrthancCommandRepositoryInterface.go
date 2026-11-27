package repository

import (
	"context"

	"api-pacs/module/orthanc/domain/entity"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepositoryInterface is the interface for orthanc command repository
type OrthancCommandRepositoryInterface interface {
	// SelectDICOMModalityByModalityID get DICOM modality by modality id
	SelectDICOMModalityByModalityID(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error)
	// DeleteDICOMModality deletes a dicom modality
	DeleteDICOMModality(ctx context.Context, tenantID, modalityID string) error
	// UpsertDICOMModality upsert DICOM modality
	UpsertDICOMModality(ctx context.Context, data repositoryTypes.UpsertDICOMModality) error
}
