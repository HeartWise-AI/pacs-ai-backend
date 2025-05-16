package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/application"
	serviceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	types "api-pacs/module/orthanc/interfaces/http"
)

// OrthancQueryController request controller for orthanc query
type OrthancQueryController struct {
	application.OrthancQueryServiceInterface
}

// GetJobsInfo get jobs information
func (controller *OrthancQueryController) GetJobsInfo(w http.ResponseWriter, r *http.Request) {
	jobIDs := r.URL.Query()["jobIds"]

	if len(jobIDs) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid job IDs.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}
	res, err := controller.OrthancQueryServiceInterface.GetJobsInfo(context.TODO(), jobIDs)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.OrthancError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Orthanc service encountered an error or timeout."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Server error."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	var jobs []types.GetJobInfoResponse

	for _, job := range res {
		jobs = append(jobs, types.GetJobInfoResponse{
			ID:       job.ID,
			Priority: job.Priority,
			Progress: job.Progress,
			State:    job.State,
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched jobs info.",
		Data:    jobs,
	}

	response.JSON(w)
}

// FindLocalSOPInstance find local SOP instance
func (controller *OrthancQueryController) FindLocalSOPInstance(w http.ResponseWriter, r *http.Request) {
	sopInstanceUID := chi.URLParam(r, "sopInstanceUID")
	if len(sopInstanceUID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid SOPInstanceUID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	queryIDs, err := controller.OrthancQueryServiceInterface.FindLocalSOPInstance(context.TODO(), sopInstanceUID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.OrthancError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Orthanc service encountered an error or timeout."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Server error."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched local SOP instance.",
		Data: &types.FindLocalSOPInstanceResponse{
			QueryIDs: queryIDs,
		},
	}

	response.JSON(w)
}

// FindModalityStudies get modality studies
func (controller *OrthancQueryController) FindModalityStudies(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.FindModalityStudiesRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	res, queryID, err := controller.OrthancQueryServiceInterface.FindModalityStudies(context.TODO(), serviceTypes.FindModalityStudies{
		TenantID:                   tenantID,
		ModalityID:                 request.ModalityID,
		UserID:                     userID,
		AccessionNumber:            request.AccessionNumber,
		InstitutionName:            request.InstitutionName,
		ModalitiesInStudy:          request.ModalitiesInStudy,
		NumberOfStudyRelatedSeries: request.NumberOfStudyRelatedSeries,
		PatientBirthDate:           request.PatientBirthDate,
		PatientID:                  request.PatientID,
		PatientName:                request.PatientName,
		PatientSex:                 request.PatientSex,
		ReferringPhysicianName:     request.ReferringPhysicianName,
		RequestingPhysician:        request.RequestingPhysician,
		StudyDate:                  request.StudyDate,
		StudyDescription:           request.StudyDescription,
		StudyID:                    request.StudyID,
		StudyInstanceUID:           request.StudyInstanceUID,
		StudyTime:                  request.StudyTime,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.OrthancError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Orthanc service encountered an error or timeout."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Server error."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	modalityStudies := types.FindModalityStudiesResponse{
		QueryID: queryID,
		Studies: []types.Study{},
	}

	for _, modalityStudy := range res {
		modalityStudies.Studies = append(modalityStudies.Studies, types.Study{
			AccessionNumber:            modalityStudy.AccessionNumber,
			ModalitiesInStudy:          modalityStudy.ModalitiesInStudy,
			NumberOfStudyRelatedSeries: modalityStudy.NumberOfStudyRelatedSeries,
			PatientBirthDate:           modalityStudy.PatientBirthDate,
			PatientID:                  modalityStudy.PatientID,
			PatientName:                modalityStudy.PatientName,
			PatientSex:                 modalityStudy.PatientSex,
			QueryRetrieveLevel:         modalityStudy.QueryRetrieveLevel,
			ReferringPhysicianName:     modalityStudy.ReferringPhysicianName,
			RetrieveAETitle:            modalityStudy.RetrieveAETitle,
			SpecificCharacterSet:       modalityStudy.SpecificCharacterSet,
			StudyDate:                  modalityStudy.StudyDate,
			StudyDescription:           modalityStudy.StudyDescription,
			StudyID:                    modalityStudy.StudyID,
			StudyInstanceUID:           modalityStudy.StudyInstanceUID,
			StudyTime:                  modalityStudy.StudyTime,
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched modality studies.",
		Data:    modalityStudies,
	}

	response.JSON(w)
}

// ListDICOMModalities list dicom modalities
func (controller *OrthancQueryController) ListDICOMModalities(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	// optional
	var modalityID *string
	modalityIDStr := r.URL.Query().Get("id")
	if len(modalityIDStr) > 0 {
		modalityID = &modalityIDStr
	}

	res, err := controller.OrthancQueryServiceInterface.ListDICOMModalities(context.TODO(), tenantID, modalityID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.OrthancError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Orthanc service encountered an error or timeout."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	listModalities := types.ListDICOMModalitiesResponse{
		Modalities: make(map[string]types.ListDICOMModality),
	}

	for key, modality := range res {
		listModalities.Modalities[key] = types.ListDICOMModality(modality)
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched dicom modalities.",
		Data:    listModalities,
	}

	response.JSON(w)
}
