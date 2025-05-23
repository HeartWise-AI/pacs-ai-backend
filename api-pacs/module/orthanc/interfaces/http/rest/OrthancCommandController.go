package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/application"
	serviceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	types "api-pacs/module/orthanc/interfaces/http"
)

// OrthancCommandController request controller for orthanc command
type OrthancCommandController struct {
	application.OrthancCommandServiceInterface
}

// RemoveDICOMModality remove dicom modality
func (controller *OrthancCommandController) RemoveDICOMModality(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	modalityID := chi.URLParam(r, "modalityID")
	if len(modalityID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid modality ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.OrthancCommandServiceInterface.RemoveDICOMModality(context.TODO(), tenantID, modalityID)
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
		Message: "Successfully removed DICOM modality.",
	}

	response.JSON(w)
}

// RetrieveModalityStudy retrieve modality study
func (controller *OrthancCommandController) RetrieveModalityStudy(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.RetrieveModalityStudyRequest

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

	res, err := controller.OrthancCommandServiceInterface.RetrieveModalityStudyBySeries(context.TODO(), serviceTypes.RetrieveModalityStudyBySeries{
		TenantID:         tenantID,
		UserID:           userID,
		ModalityID:       request.ModalityID,
		StudyInstanceUID: request.StudyInstanceUID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "Skipping retrieve."
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

	var jobs []types.RetrieveQueryModalityAnswerResponse

	for _, job := range res {
		jobs = append(jobs, types.RetrieveQueryModalityAnswerResponse{
			ID:   job.ID,
			Path: job.Path,
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully sent retrieve modality study request.",
		Data:    jobs,
	}

	response.JSON(w)
}

// TriggerDICOMEchoSCU trigger dicom C-ECHO SCU
func (controller *OrthancCommandController) TriggerDICOMEchoSCU(w http.ResponseWriter, r *http.Request) {
	modalityID := chi.URLParam(r, "modalityID")
	if len(modalityID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid modality ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.OrthancCommandServiceInterface.TriggerDICOMEchoSCU(context.TODO(), modalityID)
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
		Message: "Successfully sent DICOM C-ECHO SCU request.",
	}

	response.JSON(w)
}

// UpdateDICOMModality update dicom modality
func (controller *OrthancCommandController) UpdateDICOMModality(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	modalityID := chi.URLParam(r, "modalityID")
	if len(modalityID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid modality ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.UpdateDICOMModalityRequest

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

	err = controller.OrthancCommandServiceInterface.UpdateDICOMModality(context.TODO(), serviceTypes.UpdateDICOMModality{
		TenantID:      tenantID,
		ModalityID:    modalityID,
		AET:           request.AET,
		Host:          request.Host,
		Port:          request.Port,
		UseDicomTLS:   request.UseDicomTLS,
		CFindEnabled:  request.CFindEnabled,
		CMoveEnabled:  request.CMoveEnabled,
		CStoreEnabled: request.CStoreEnabled,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore error."
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
		Message: "Successfully updated DICOM modality.",
	}

	response.JSON(w)
}

// StoreStudyCustomSeries store study custom series
func (controller *OrthancCommandController) StoreStudyCustomSeries(w http.ResponseWriter, r *http.Request) {
	// tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	// get modality ID
	modalityID := chi.URLParam(r, "modalityID")
	if len(modalityID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid modality ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// get study instance UID
	studyInstanceUID := chi.URLParam(r, "studyInstanceUID")
	if len(studyInstanceUID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid study instance UID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.StoreStudyCustomSeriesRequest

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

	// TODO: implementation

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully stored study custom series.",
	}

	response.JSON(w)
}
