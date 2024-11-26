package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	types "api-pacs/module/inference/interfaces/http"
)

// InferenceQueryController request controller for inference query
type InferenceQueryController struct {
	application.InferenceQueryServiceInterface
}

// GetContainerInfo returns the inference model container info
func (controller *InferenceQueryController) GetContainerInfo(w http.ResponseWriter, r *http.Request) {
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

	containerInfo, err := controller.InferenceQueryServiceInterface.GetContainerInfo(r.Context(), containerID)
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
		Message: "Successfully retrieved inference model container info.",
		Data: &types.GetContainerInfoResponse{
			ID:         containerInfo.ID,
			Name:       containerInfo.Name,
			Status:     containerInfo.Status,
			Running:    containerInfo.Running,
			StartedAt:  uint64(containerInfo.StartedAt.Unix()),
			FinishedAt: uint64(containerInfo.FinishedAt.Unix()),
		},
	}

	response.JSON(w)
}

// GetInferenceModels returns the inference models
func (controller *InferenceQueryController) GetInferenceModels(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	inferenceModels, err := controller.InferenceQueryServiceInterface.GetInferenceModels(r.Context(), tenantID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore service encountered an error."
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

	var inferenceModelsResponse []types.GetInferenceModelResponse
	for _, inferenceModel := range inferenceModels {
		// set env to empty if nil
		envs := inferenceModel.Envs
		if envs == nil {
			envs = []string{}
		}

		inferenceModelsResponse = append(inferenceModelsResponse, types.GetInferenceModelResponse{
			ID:       inferenceModel.ID,
			TenantID: inferenceModel.TenantID,
			ContainerInfo: types.GetContainerInfoResponse{
				ID:              inferenceModel.ContainerInfo.ID,
				Name:            inferenceModel.ContainerInfo.Name,
				Status:          inferenceModel.ContainerInfo.Status,
				Running:         inferenceModel.ContainerInfo.Running,
				StartedAt:       uint64(inferenceModel.ContainerInfo.StartedAt.Unix()),
				FinishedAt:      uint64(inferenceModel.ContainerInfo.FinishedAt.Unix()),
				CPUPercentUsage: inferenceModel.ContainerInfo.CPUPercentUsage,
				MemoryInBytes:   inferenceModel.ContainerInfo.MemoryInBytes,
			},
			Name:        inferenceModel.Name,
			DockerImage: inferenceModel.DockerImage,
			Envs:        envs,
			OutputMode:  inferenceModel.OutputMode,
			CreatedAt:   uint64(inferenceModel.CreatedAt.Unix()),
			UpdatedAt:   uint64(inferenceModel.UpdatedAt.Unix()),
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved inference models.",
		Data:    inferenceModelsResponse,
	}

	response.JSON(w)
}
