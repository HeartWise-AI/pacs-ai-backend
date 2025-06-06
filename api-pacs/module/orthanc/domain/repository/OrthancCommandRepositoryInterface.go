package repository

import (
	"context"

	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepositoryInterface is the interface for orthanc command repository
type OrthancCommandRepositoryInterface interface {
	// DeleteDICOMModality deletes a dicom modality
	DeleteDICOMModality(ctx context.Context, tenantID, modalityID string) error
	// UpsertDICOMModality upsert DICOM modality
	UpsertDICOMModality(ctx context.Context, data repositoryTypes.UpsertDICOMModality) error
}
