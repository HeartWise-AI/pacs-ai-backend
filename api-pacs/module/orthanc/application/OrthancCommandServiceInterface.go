package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancCommandServiceInterface holds the implementable methods for the Orthanc command service
type OrthancCommandServiceInterface interface {
	// ClearLocalStudiesCache clear local studies cache
	ClearLocalStudiesCache(ctx context.Context) error
	// RemoveDICOMModality remove dicom modality
	RemoveDICOMModality(ctx context.Context, tenantID string, modalityID string) error
	// RetrieveModalityStudyBySeries retrieve modality study by series
	RetrieveModalityStudyBySeries(ctx context.Context, data types.RetrieveModalityStudyBySeries) ([]orthancAPITypes.QueryModalityResponse, error)
	// StoreStudyCustomSeries store study custom series
	StoreStudyCustomSeries(ctx context.Context, data types.StoreStudyCustomSeries) error
	// TriggerDICOMEchoSCU trigger dicom echo scu
	TriggerDICOMEchoSCU(ctx context.Context, modalityID string) error
	// UpdateDICOMModality update dicom modality
	UpdateDICOMModality(ctx context.Context, data types.UpdateDICOMModality) error
}
