package repository

import (
	"context"

	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepositoryInterface is the interface for orthanc command repository
type OrthancCommandRepositoryInterface interface {
	// UpsertDICOMModality upsert DICOM modality
	UpsertDICOMModality(ctx context.Context, data repositoryTypes.UpsertDICOMModality) error
}
