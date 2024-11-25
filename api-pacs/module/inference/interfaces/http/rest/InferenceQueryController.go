package rest

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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
