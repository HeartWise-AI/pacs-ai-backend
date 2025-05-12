package types

import (
	"context"
)

// OrthancAPIInterface list of implementable methods for orthanc api
type OrthancAPIInterface interface {
	// DeleteDICOMModality deletes a DICOM modality
	DeleteDICOMModality(ctx context.Context, modalityID string) error
	// DeleteLocalResources deletes local resources
	DeleteLocalResources(ctx context.Context, request DeleteLocalResourcesRequest) error
	// DownloadDICOM downloads a DICOM file
	DownloadDICOM(ctx context.Context, queryID string) ([]byte, error)
	// FindLocalResources finds local resources
	FindLocalResources(ctx context.Context, request QueryLocalResourceRequest) ([]GetLocalResourceResponse, error)
	// FindLocalSOPInstance finds local SOP instances
	FindLocalSOPInstance(ctx context.Context, sopInstanceUID string) ([]string, error)
	// FindModalitySeriesByStudy finds modality series by study
	FindModalitySeriesByStudy(ctx context.Context, modalityID, localAET, studyInstanceUID string) ([]QueryModalitySeriesAnswersResponse, error)
	// FindModalityStudies finds modality studies
	FindModalityStudies(ctx context.Context, modalityID string, request QueryModalitiesRequest) ([]QueryModalityStudyAnswersResponse, string, error)
	// GetJobInfo gets job info
	GetJobInfo(ctx context.Context, jobID string) (GetJobResponse, error)
	// GetDICOMWebSeriesInstances gets DICOM web series instances
	GetDICOMWebSeriesInstances(ctx context.Context, studyInstanceUID, seriesInstanceUID string) ([]map[string]interface{}, error)
	// ListDICOMModalities lists DICOM modalities
	ListDICOMModalities(ctx context.Context) (map[string]ListDICOMModalitiesResponse, error)
	// RetrieveModalityStudy retrieves modality study
	RetrieveModalityStudy(ctx context.Context, queryID string, answerIndex uint, request RetrieveQueryModalityAnswerRequest) (QueryModalityResponse, error)
	// RetrieveModalityStudyBySeries retrieves modality study by series
	RetrieveModalityStudyBySeries(ctx context.Context, modalityID, localAet, studyInstanceUID string) ([]QueryModalityResponse, error)
	// RetrieveModalityStudyByInstances retrieves modality study by instances (for US modalities)
	RetrieveModalityStudyByInstances(ctx context.Context, modalityID, localAet, studyInstanceUID string) ([]QueryModalityResponse, error)
	// RetrieveDICOMWebInstanceFile retrieves DICOM web instance file
	RetrieveDICOMWebInstanceFile(ctx context.Context, studyInstanceUID, seriesInstanceUID, sopInstanceUID string) ([]byte, error)
	// RetrieveDICOMWebInstanceMetadata retrieves DICOM web instance metadata
	RetrieveDICOMWebInstanceMetadata(ctx context.Context, studyInstanceUID, seriesInstanceUID, sopInstanceUID string) ([]map[string]interface{}, error)
	// TriggerDICOMEchoSCU trigger dicom C-ECHO SCU
	TriggerDICOMEchoSCU(ctx context.Context, modalityID string) error
	// UpdateDICOMModality updates a DICOM modality
	UpdateDICOMModality(ctx context.Context, modalityID string, request UpdateDICOMModalityRequest) error
}
