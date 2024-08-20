package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/application"
	serviceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	types "api-pacs/module/orthanc/interfaces/http"
)

// OrthancQueryController request controller for orthanc query
type OrthancQueryController struct {
	application.OrthancQueryServiceInterface
}

// GetJobInfo get job information
func (controller *OrthancQueryController) GetJobInfo(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if len(jobID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid job ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	res, err := controller.OrthancQueryServiceInterface.GetJobInfo(context.TODO(), jobID)
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
		Message: "Successfully fetched job info.",
		Data: &types.GetJobInfoResponse{
			ID:       res.ID,
			Priority: res.Priority,
			Progress: res.Progress,
			State:    res.State,
		},
	}

	response.JSON(w)
}

// FindLocalResource find local resource.
func (controller *OrthancQueryController) FindLocalResource(w http.ResponseWriter, r *http.Request) {
	var request types.FindLocalResourceRequest

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

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	queryIDs, err := controller.OrthancQueryServiceInterface.FindLocalResource(context.TODO(), serviceTypes.FindLocalResource{
		Level: request.Level,
		Query: struct {
			StudyInstanceUID string
			SOPInstanceUID   string
		}{
			StudyInstanceUID: request.Query.StudyInstanceUID,
			SOPInstanceUID:   request.Query.SOPInstanceUID,
		},
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

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched local resource.",
		Data: &types.FindLocalResourceResponse{
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
	if err != nil && err.Error() != errors.MissingRecord {
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
