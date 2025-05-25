package application

import (
	"context"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/domain/entity"
	"api-pacs/module/orthanc/infrastructure/service/types"
)

// OrthancQueryServiceInterface holds the implementable methods for the Orthanc query service
type OrthancQueryServiceInterface interface {
	// FindLocalSOPInstance find local sop instance
	FindLocalSOPInstance(ctx context.Context, sopInstanceUID string) ([]string, error)
	// FindModalityStudies find modality studies
	FindModalityStudies(ctx context.Context, data types.FindModalityStudies) ([]orthancAPITypes.QueryModalityStudyAnswersResponse, string, error)
	// GetJobsInfo get jobs info
	GetJobsInfo(ctx context.Context, jobIDs []string) ([]orthancAPITypes.GetJobResponse, error)
	// GetLinkedDICOMModalityWithEnabledCStore get linked DICOM modality with enabled C-Store
	GetLinkedDICOMModalityWithEnabledCStore(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error)
	// ListDICOMModalities list DICOM modalities
	ListDICOMModalities(ctx context.Context, tenantID string) (map[string]types.ListDICOMModalityResult, error)
}
