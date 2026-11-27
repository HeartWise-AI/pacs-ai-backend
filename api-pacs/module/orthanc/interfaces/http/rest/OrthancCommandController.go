package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
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
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "DICOM modality not found."
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
		ModalityID:       request.ModalityID,
		StudyInstanceUID: request.StudyInstanceUID,
		ModalityType:     request.ModalityType,
		UserID:           &userID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "DICOM modality not found."
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
			ID:                job.ID,
			Path:              job.Path,
			SeriesInstanceUID: job.SeriesInstanceUID,
			SeriesNumber:      job.SeriesNumber,
			Order:             job.Order,
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
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)

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

	err := controller.OrthancCommandServiceInterface.TriggerDICOMEchoSCU(context.TODO(), tenantID, modalityID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "DICOM modality not found."
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
		case apiError.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "DICOM modality not found."
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

// StoreStudyCustomSeries sends a registered model-run report (form field
// executionID) back to one of the tenant's C-STORE modalities. It needs
// processing.manage with access to every model. Caller-supplied files and
// identity fields are never stored: raw DICOM is refused and the PDF and
// identity come from the registered run.
func (controller *OrthancCommandController) StoreStudyCustomSeries(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID, _ := r.Context().Value(iamTypes.UserIDCtx).(string)
	refuse := func(status int, message, code string) {
		response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: code}
		response.JSON(w)
	}

	access, err := accessctx.Get(r.Context())
	if err != nil {
		refuse(http.StatusServiceUnavailable, "Access verification is temporarily unavailable.", apiError.AccessUnavailable)
		return
	}
	if !access.Has(accessEntity.PermProcessingManage) || !access.AllModels {
		refuse(http.StatusForbidden, "Forbidden access.", apiError.ForbiddenAccess)
		return
	}

	modalityID := chi.URLParam(r, "modalityID")
	if tenantID == "" || len(modalityID) == 0 {
		refuse(http.StatusBadRequest, "Invalid modality ID.", apiError.InvalidRequestPayload)
		return
	}

	// A legacy client may still attach its own rendering; only a PDF is
	// tolerated and it is ignored. Raw DICOM is refused for every role.
	if file, fileHeader, err := r.FormFile("file"); err == nil {
		file.Close()
		if fileHeader.Header.Get("Content-Type") != "application/pdf" {
			refuse(http.StatusBadRequest, "Only registered model reports can be stored.", apiError.InvalidPayload)
			return
		}
	}

	executionID := strings.TrimSpace(r.FormValue("executionID"))
	if executionID == "" {
		refuse(http.StatusBadRequest, "Invalid execution ID.", apiError.InvalidRequestPayload)
		return
	}

	err = controller.OrthancCommandServiceInterface.StoreStudyCustomSeries(r.Context(), serviceTypes.StoreStudyCustomSeries{
		TenantID:    tenantID,
		UserID:      userID,
		ModalityID:  modalityID,
		ExecutionID: executionID,
	})
	if err != nil {
		switch err.Error() {
		case apiError.InvalidPayload, apiError.InvalidRequestPayload, apiError.DICOMParseError:
			refuse(http.StatusBadRequest, "Invalid store request.", err.Error())
		case apiError.MissingRecord:
			refuse(http.StatusNotFound, "No C-STORE modality or registered report found.", err.Error())
		case apiError.ForbiddenAccess:
			refuse(http.StatusForbidden, "C-STORE is not enabled for this modality.", err.Error())
		case apiError.DuplicateRecord:
			refuse(http.StatusConflict, "Report already exist for this study and model version.", err.Error())
		case apiError.OrthancError:
			refuse(http.StatusInternalServerError, "Orthanc service encountered an error or timeout.", err.Error())
		default:
			refuse(http.StatusInternalServerError, "Server error.", err.Error())
		}
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully stored study custom series.",
	}
	response.JSON(w)
}
