package rest

import (
	"context"
	"errors"
	"net/http"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/service"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	httpTypes "api-pacs/module/inference/interfaces/http"
	"github.com/go-chi/chi/v5"
)

type GPUAdministration interface {
	Snapshot(context.Context, string) (service.GPUAdministrationSnapshot, error)
	Move(context.Context, string, string, string, string, string) (serviceTypes.DeployInferenceModelVersionResult, error)
}

func (controller *InferenceCommandController) GetGPUs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	if controller.GPUAdministration == nil {
		writeManagedInferenceResponse(w, http.StatusServiceUnavailable, "GPU administration is unavailable.", apiError.InferenceUpgradeUnavailable)
		return
	}
	result, err := controller.GPUAdministration.Snapshot(r.Context(), tenantID)
	if err != nil {
		writeGPUAdministrationError(w, err)
		return
	}
	response := viewmodels.HTTPResponseVM{Status: http.StatusOK, Success: true, Message: "GPU status retrieved.", Data: result}
	response.JSON(w)
}
func (controller *InferenceCommandController) MoveInferenceModelGPU(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	actor, _ := r.Context().Value(iamTypes.UserIDCtx).(string)
	var body struct {
		GPUUUID             string `json:"gpuUuid"`
		ExpectedContainerID string `json:"expectedContainerId"`
	}
	if err := requestbody.DecodeJSON(w, r, &body, 4096); err != nil {
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid GPU placement request.", apiError.InvalidPayload)
		return
	}
	if controller.GPUAdministration == nil {
		writeGPUAdministrationError(w, errors.New(apiError.InferenceUpgradeUnavailable))
		return
	}
	result, err := controller.GPUAdministration.Move(r.Context(), tenantID, actor, chi.URLParam(r, "ID"), body.GPUUUID, body.ExpectedContainerID)
	if err != nil {
		writeGPUAdministrationError(w, err)
		return
	}
	response := viewmodels.HTTPResponseVM{Status: http.StatusAccepted, Success: true, Message: "GPU placement deployment accepted.", Data: httpTypes.DeployInferenceModelVersionResponse{DeploymentID: result.Operation.ID, Status: result.Operation.Status, Stage: result.Operation.Stage}}
	response.JSON(w)
}
func writeGPUAdministrationError(w http.ResponseWriter, err error) {
	status, message, code := modelUpgradeHTTPError(err)
	if err.Error() == apiError.InvalidPayload {
		status = http.StatusBadRequest
		message = "GPU placement is invalid or unsupported."
	}
	if status == http.StatusInternalServerError {
		message = "GPU administration is temporarily unavailable."
		code = apiError.ServerError
	}
	writeManagedInferenceResponse(w, status, message, code)
}
