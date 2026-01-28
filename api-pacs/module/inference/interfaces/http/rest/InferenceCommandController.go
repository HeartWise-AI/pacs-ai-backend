package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

// InferenceCommandController request controller for inference command
type InferenceCommandController struct {
	application.InferenceCommandServiceInterface
}

// AddInferenceModel add a new inference model
func (controller *InferenceCommandController) AddInferenceModel(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.AddInferenceModelRequest

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

	err = controller.InferenceCommandServiceInterface.AddInferenceModel(context.TODO(), serviceTypes.AddInferenceModel{
		TenantID:    tenantID,
		Name:        request.Name,
		DockerImage: request.DockerImage,
		Envs:        request.Envs,
		OutputMode:  request.OutputMode,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference model."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully added inference model.",
	}

	response.JSON(w)
}

// DeleteInferenceModel delete an inference model
func (controller *InferenceCommandController) DeleteInferenceModel(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid inference model ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.DeleteInferenceModel(context.TODO(), ID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference model."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully deleted inference model.",
	}

	response.JSON(w)
}

// RemoveModelFeedback removes model feedback
func (controller *InferenceCommandController) RemoveModelFeedback(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	err := controller.InferenceCommandServiceInterface.DeleteModelFeedback(context.TODO(), userID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while removing model feedback."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully removed model feedback.",
	}

	response.JSON(w)
}

// PredictInferenceModel predicts an inference model
func (controller *InferenceCommandController) PredictInferenceModel(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.PredictInferenceModelRequest
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

	// check if empty
	if len(request.SeriesInstanceUIDs) == 0 {
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
	sort.Slice(request.SeriesInstanceUIDs, func(i, j int) bool {
		partsI := strings.Split(request.SeriesInstanceUIDs[i], ".")
		lastPartI, err := strconv.ParseInt(partsI[len(partsI)-1], 10, 64)
		if err != nil {
			return false
		}

		partsJ := strings.Split(request.SeriesInstanceUIDs[j], ".")
		lastPartJ, err := strconv.ParseInt(partsJ[len(partsJ)-1], 10, 64)
		if err != nil {
			return false
		}

		return lastPartI < lastPartJ
	})

	predictionResult, err := controller.InferenceCommandServiceInterface.PredictInferenceModel(context.TODO(), tenantID, userID, containerID, serviceTypes.PredictInferenceModel{
		StudyInstanceUID:   request.StudyInstanceUID,
		SeriesInstanceUIDs: request.SeriesInstanceUIDs,
		AdditionalMetadata: request.AdditionalMetadata,
		ForceJSON:          request.ForceJSON,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case apiError.DockerInferenceError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker inference service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully applied prediction to inference model.",
		Data:    predictionResult.Data,
	}

	response.JSON(w)
}

// RestartInferenceModelContainer restarts an inference model container
func (controller *InferenceCommandController) RestartInferenceModelContainer(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.RestartInferenceModelContainer(context.TODO(), containerID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully restarted inference model container.",
	}

	response.JSON(w)
}

// StartInferenceModelContainer starts an inference model container
func (controller *InferenceCommandController) StartInferenceModelContainer(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.StartInferenceModelContainer(context.TODO(), containerID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully started inference model container.",
	}

	response.JSON(w)
}

// StopInferenceModelContainer stops an inference model container
func (controller *InferenceCommandController) StopInferenceModelContainer(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerID")
	if len(containerID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid container ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.StopInferenceModelContainer(context.TODO(), containerID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully stopped inference model container.",
	}

	response.JSON(w)
}

// UpdateInferenceModel update an inference model
func (controller *InferenceCommandController) UpdateInferenceModel(w http.ResponseWriter, r *http.Request) {
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid inference model ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.UpdateInferenceModelRequest

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

	err = controller.InferenceCommandServiceInterface.UpdateInferenceModel(context.TODO(), serviceTypes.UpdateInferenceModel{
		ID:                  ID,
		DisallowedDICOMTags: request.DisallowedDICOMTags,
		OutputMode:          request.OutputMode,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving inference model."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully updated inference model.",
	}

	response.JSON(w)
}

// UpdateModelFeedback updates model feedback
func (controller *InferenceCommandController) UpdateModelFeedback(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	// required
	ID := chi.URLParam(r, "ID")
	if len(ID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid model feedback ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.UpdateModelFeedbackRequest

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

	var modelFeedbackAnswer *serviceTypes.ModelFeedbackAnswer

	// if feedback type is reject, include model feedback answer
	if request.FeedbackType == entity.RejectFeedbackType {
		if request.ModelFeedbackAnswer == nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Model feedback answer is required for reject feedback type.",
				ErrorCode: apiError.InvalidRequestPayload,
			}

			response.JSON(w)
			return
		}

		modelFeedbackAnswer = &serviceTypes.ModelFeedbackAnswer{
			ModelFeedbackID:        ID,
			QuestionnaireID:        request.ModelFeedbackAnswer.QuestionnaireID,
			QuestionnaireQuestion:  request.ModelFeedbackAnswer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: request.ModelFeedbackAnswer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   request.ModelFeedbackAnswer.QuestionnaireAnswers,
		}
	}

	err = controller.InferenceCommandServiceInterface.UpdateModelFeedback(context.TODO(), serviceTypes.UpdateModelFeedback{
		ID:                  ID,
		TenantID:            tenantID,
		UserID:              userID,
		ModelID:             request.ModelID,
		FeedbackType:        request.FeedbackType,
		ModelFeedbackAnswer: modelFeedbackAnswer,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating model feedback."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
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
		Message: "Successfully updated model feedback.",
	}

	response.JSON(w)
}
