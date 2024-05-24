package service

import (
	"context"
	"os"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/orthanc/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
)

// OrthancQueryService handles the Orthanc query service logic
type OrthancQueryService struct {
	orthancAPITypes.OrthancAPIInterface
	tenantApplication.TenantQueryServiceInterface
}

// GetJobInfo get job info
func (service *OrthancQueryService) GetJobInfo(ctx context.Context, jobID string) (orthancAPITypes.GetJobResponse, error) {
	res, err := service.OrthancAPIInterface.GetJobInfo(ctx, jobID)
	if err != nil {
		return orthancAPITypes.GetJobResponse{}, err
	}

	return res, nil
}

// GetModalityStudies get modality studies
func (service *OrthancQueryService) GetModalityStudies(ctx context.Context, tenantID string, data types.GetModalityStudies) ([]orthancAPITypes.QueryModalitiesAnswersResponse, string, error) {
	// get tenant info
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}

	res, queryID, err := service.OrthancAPIInterface.GetModalityStudies(ctx, tenant.AET, orthancAPITypes.QueryModalitiesRequest{
		Level:     "Study",
		LocalAET:  os.Getenv("ORTHANC_AET"),
		Normalize: true,
		Query: orthancAPITypes.QueryStudy{
			AccessionNumber:            data.AccessionNumber,
			InstitutionName:            data.InstitutionName,
			ModalitiesInStudy:          data.ModalitiesInStudy,
			NumberOfStudyRelatedSeries: data.NumberOfStudyRelatedSeries,
			PatientBirthDate:           data.PatientBirthDate,
			PatientID:                  data.PatientID,
			PatientName:                data.PatientName,
			PatientSex:                 data.PatientSex,
			ReferringPhysicianName:     data.ReferringPhysicianName,
			RequestingPhysician:        data.RequestingPhysician,
			StudyDate:                  data.StudyDate,
			StudyDescription:           data.StudyDescription,
			StudyID:                    data.StudyID,
			StudyInstanceUID:           data.StudyInstanceUID,
			StudyTime:                  data.StudyTime,
		},
		Timeout: 0,
	})
	if err != nil {
		return nil, "", err
	}

	return res, queryID, nil
}
