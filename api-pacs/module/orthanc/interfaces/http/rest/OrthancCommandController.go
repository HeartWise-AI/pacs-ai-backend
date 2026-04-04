package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

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

var inputSeriesAllowedFileTypes = []string{"application/pdf", "application/dicom"}

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
		ModalityID:       request.ModalityID,
		StudyInstanceUID: request.StudyInstanceUID,
		ModalityType:     request.ModalityType,
		UserID:           &userID,
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
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

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

	/// get file
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		log.Println("Cannot read file:", err)
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Cannot read file.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}
	defer file.Close()

	mimeType := fileHeader.Header.Get("Content-Type")

	// limit allowed mime type only
	var isMimeTypeAllowed bool
	for _, allowedInputSeriesFileType := range inputSeriesAllowedFileTypes {
		if mimeType == allowedInputSeriesFileType {
			isMimeTypeAllowed = true
		}
	}

	if !isMimeTypeAllowed {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid input series file type.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	// copy content and assign
	var buf bytes.Buffer
	io.Copy(&buf, file)
	fileBody := buf.String()

	/// get metadata
	// series instance UIDs
	seriesInstanceUIDsStr := r.FormValue("seriesInstanceUIDs")
	if len(seriesInstanceUIDsStr) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid series instance UIDs.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var seriesInstanceUIDs []string
	if err := json.Unmarshal([]byte(seriesInstanceUIDsStr), &seriesInstanceUIDs); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid series instance UIDs.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// check if empty
	if len(seriesInstanceUIDs) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Series instance UIDs is empty.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// sort series instance by last part asc
	sort.Slice(seriesInstanceUIDs, func(i, j int) bool {
		partsI := strings.Split(seriesInstanceUIDs[i], ".")
		lastPartI, err := strconv.ParseInt(partsI[len(partsI)-1], 10, 64)
		if err != nil {
			return false
		}

		partsJ := strings.Split(seriesInstanceUIDs[j], ".")
		lastPartJ, err := strconv.ParseInt(partsJ[len(partsJ)-1], 10, 64)
		if err != nil {
			return false
		}

		return lastPartI < lastPartJ
	})

	// patient ID
	patientID := r.FormValue("patientID")
	if len(patientID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid patient ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// patient name
	patientName := r.FormValue("patientName")
	if len(patientName) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid patient name.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// model name
	modelName := r.FormValue("modelName")
	if len(modelName) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid model name.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// model version
	modelVersion := r.FormValue("modelVersion")
	if len(modelVersion) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid model version.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.OrthancCommandServiceInterface.StoreStudyCustomSeries(context.TODO(), serviceTypes.StoreStudyCustomSeries{
		TenantID:           tenantID,
		UserID:             userID,
		ModalityID:         modalityID,
		StudyInstanceUID:   studyInstanceUID,
		SeriesInstanceUIDs: seriesInstanceUIDs,
		PatientID:          patientID,
		PatientName:        patientName,
		ModelName:          modelName,
		ModelVersion:       modelVersion,
		FileBody:           []byte(fileBody),
		FileMimeType:       mimeType,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.OrthancError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Orthanc service encountered an error or timeout."
		case apiError.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "Report already exist for this study and model version."
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
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully stored study custom series.",
	}

	response.JSON(w)
}
