package rest

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelupgrade"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

// PublishInferenceModelVersion registers one immutable release artifact. This
// route is protected by the Firebase superuser key in the router.
func (controller *InferenceCommandController) PublishInferenceModelVersion(w http.ResponseWriter, r *http.Request) {
	var request types.PublishInferenceModelVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || types.Validate.Struct(request) != nil {
		response := viewmodels.HTTPResponseVM{
			Status: http.StatusBadRequest, Success: false, Message: "Invalid model version payload.",
			ErrorCode: apiError.InvalidPayload,
		}
		response.JSON(w)
		return
	}
	published, err := controller.InferenceCommandServiceInterface.PublishInferenceModelVersion(r.Context(), entity.InferenceModelVersion{
		ModelID: request.ModelID, Version: request.Version, ImageReference: request.ImageReference,
		ImageDigest: request.ImageDigest, Channel: request.Channel, BaseVersion: request.BaseVersion,
		SourceBranch: request.SourceBranch, PullRequest: request.PullRequest, WorkflowRun: request.WorkflowRun,
		Provenance: request.Provenance, Status: entity.InferenceModelVersionAvailable,
	})
	if err != nil {
		statusCode := http.StatusInternalServerError
		message := "Could not publish inference model version."
		if err.Error() == apiError.InvalidPayload {
			statusCode = http.StatusBadRequest
		} else if err.Error() == apiError.InferenceUpgradeConflict {
			statusCode = http.StatusConflict
		}
		response := viewmodels.HTTPResponseVM{Status: statusCode, Success: false, Message: message, ErrorCode: err.Error()}
		response.JSON(w)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusCreated, Success: true, Message: "Successfully published inference model version.",
		Data: inferenceModelVersionResponse(published, false),
	}
	response.JSON(w)
}

// DeployInferenceModelVersion activates the selected catalog version. An older
// version follows the same path and therefore acts as a manual rollback.
func (controller *InferenceCommandController) DeployInferenceModelVersion(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	actorUserID, _ := r.Context().Value(iamTypes.UserIDCtx).(string)
	modelID := chi.URLParam(r, "ID")
	versionID := chi.URLParam(r, "versionID")
	if tenantID == "" || actorUserID == "" || modelID == "" || versionID == "" {
		response := viewmodels.HTTPResponseVM{
			Status: http.StatusBadRequest, Success: false, Message: "Invalid model deployment request.",
			ErrorCode: apiError.InvalidPayload,
		}
		response.JSON(w)
		return
	}
	result, err := controller.InferenceCommandServiceInterface.DeployInferenceModelVersion(r.Context(), serviceTypes.DeployInferenceModelVersion{
		TenantID: tenantID, ModelID: modelID, VersionID: versionID, ActorUserID: actorUserID,
	})
	if err != nil {
		statusCode, message, errorCode := modelUpgradeHTTPError(err)
		response := viewmodels.HTTPResponseVM{Status: statusCode, Success: false, Message: message, ErrorCode: errorCode}
		response.JSON(w)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusAccepted, Success: true, Message: "Inference model deployment accepted.",
		Data: types.DeployInferenceModelVersionResponse{
			DeploymentID: result.Operation.ID, Status: result.Operation.Status, Stage: result.Operation.Stage,
		},
	}
	response.JSON(w)
}

// GetInferenceModelDeploymentOperation returns persisted progress for one deployment.
func (controller *InferenceCommandController) GetInferenceModelDeploymentOperation(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	modelID := chi.URLParam(r, "ID")
	deploymentID := chi.URLParam(r, "deploymentID")
	operation, err := controller.InferenceCommandServiceInterface.GetInferenceModelDeploymentOperation(
		r.Context(), serviceTypes.GetInferenceModelDeploymentOperation{
			TenantID: tenantID, ModelID: modelID, DeploymentID: deploymentID,
		},
	)
	if err != nil {
		statusCode := http.StatusInternalServerError
		message := "Could not retrieve inference model deployment."
		switch err.Error() {
		case apiError.InvalidPayload:
			statusCode = http.StatusBadRequest
		case apiError.MissingRecord:
			statusCode = http.StatusNotFound
		case apiError.InferenceUpgradeUnavailable:
			statusCode = http.StatusServiceUnavailable
		}
		response := viewmodels.HTTPResponseVM{Status: statusCode, Success: false, Message: message, ErrorCode: err.Error()}
		response.JSON(w)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true, Message: "Successfully retrieved inference model deployment.",
		Data: types.GetInferenceModelDeploymentOperationResponse{Deployment: operation},
	}
	response.JSON(w)
}

func modelUpgradeHTTPError(err error) (int, string, string) {
	errorCode := err.Error()
	var upgradeErr *modelupgrade.UpgradeError
	if stdErrors.As(err, &upgradeErr) {
		errorCode = upgradeErr.Err.Error()
	}
	switch errorCode {
	case apiError.InvalidPayload:
		return http.StatusBadRequest, "Invalid model deployment request.", errorCode
	case apiError.MissingRecord:
		return http.StatusNotFound, "Inference model or version was not found.", errorCode
	case apiError.InferenceUpgradeConflict:
		return http.StatusConflict, "Another deployment changed or is updating this model.", errorCode
	case apiError.InferenceUpgradeUnavailable:
		return http.StatusServiceUnavailable, "Managed model deployment is unavailable.", errorCode
	case apiError.InferenceCandidateDeploymentDisabled:
		return http.StatusForbidden, "Candidate model deployment is disabled in this environment.", errorCode
	default:
		return http.StatusInternalServerError, "Inference model deployment failed and was rolled back.", errorCode
	}
}

// InferenceCommandController request controller for inference command
type InferenceCommandController struct {
	application.InferenceCommandServiceInterface
	application.ModelDeploymentOperationSubscriberInterface
	ModelDeploymentEventHeartbeatInterval time.Duration
}

var mediaMaxFileSize int64 = 5 * 1024 * 1024 // 5MB

const mediaMultipartOverheadAllowance int64 = 64 * 1024

const defaultInferencePredictMaxRequestBodyBytes int64 = 1024 * 1024

var mediaAllowedFileTypes = []string{
	"text/csv", "application/csv",
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
	predictMaxBytes := requestbody.PositiveInt64FromEnvironment(
		"INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES",
		defaultInferencePredictMaxRequestBodyBytes,
	)
	if err := requestbody.DecodeJSON(w, r, &request, predictMaxBytes); err != nil {
		if requestbody.IsTooLarge(err) {
			requestbody.ObserveRejection(r, predictMaxBytes, "inference_predict")
			requestbody.WriteTooLarge(w)
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

	predictionResult, err := controller.InferenceCommandServiceInterface.PredictInferenceModel(r.Context(), tenantID, containerID, &userID, serviceTypes.PredictInferenceModel{
		StudyInstanceUID:   request.StudyInstanceUID,
		SeriesInstanceUIDs: request.SeriesInstanceUIDs,
		AdditionalMetadata: request.AdditionalMetadata,
		ForceJSON:          request.ForceJSON,
	})
	if err != nil {
		if writeInferenceManagerError(w, err) {
			return
		}
		if writeInferenceQuotaError(w, err) {
			return
		}
		if writeInferenceInputError(w, err) {
			return
		}
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DockerError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker service encountered an error."
		case apiError.DockerInferenceError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Docker inference service encountered an error."
		case apiError.InferenceQuotaUnavailable:
			httpCode = http.StatusServiceUnavailable
			errorMsg = "Inference quota enforcement is temporarily unavailable."
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

// RemoveInferenceModel deletes an inference model
func (controller *InferenceCommandController) RemoveInferenceModel(w http.ResponseWriter, r *http.Request) {
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

	err := controller.InferenceCommandServiceInterface.RemoveInferenceModel(context.TODO(), ID)
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
