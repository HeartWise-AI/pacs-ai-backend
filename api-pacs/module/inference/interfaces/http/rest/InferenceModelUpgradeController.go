package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	modelupgrade "api-pacs/module/inference/infrastructure/modelupgrade"
	types "api-pacs/module/inference/interfaces/http"
)

func (controller *InferenceCommandController) StartInferenceModelUpgrade(w http.ResponseWriter, r *http.Request) {
	if controller.InferenceModelUpgradeServiceInterface == nil {
		writeModelUpgradeUnavailable(w)
		return
	}
	modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID, _ := r.Context().Value(iamTypes.UserIDCtx).(string)
	var request types.StartInferenceModelUpgradeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&request)
	var trailing interface{}
	trailingErr := decoder.Decode(&trailing)
	if modelID == "" || decodeErr != nil || trailingErr != io.EOF || strings.TrimSpace(request.ImageReference) == "" {
		response := viewmodels.HTTPResponseVM{
			Status: http.StatusBadRequest, Success: false, Message: "Invalid model upgrade request.", ErrorCode: apiError.InvalidRequestPayload,
		}
		response.JSON(w)
		return
	}
	upgrade, err := controller.InferenceModelUpgradeServiceInterface.StartInferenceModelUpgrade(
		r.Context(),
		application.StartInferenceModelUpgrade{
			TenantID: tenantID, ModelID: modelID, ActorUserID: userID,
			ImageReference: request.ImageReference, ExpectedDigest: request.ExpectedDigest,
			DrainTimeoutSeconds: request.DrainTimeoutSeconds,
		},
	)
	if err != nil {
		writeModelUpgradeError(w, err)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusAccepted, Success: true, Message: "Model upgrade accepted.", Data: upgrade,
	}
	response.JSON(w)
}

func (controller *InferenceCommandController) GetInferenceModelUpgrade(w http.ResponseWriter, r *http.Request) {
	if controller.InferenceModelUpgradeServiceInterface == nil {
		writeModelUpgradeUnavailable(w)
		return
	}
	modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
	upgradeID := strings.TrimSpace(chi.URLParam(r, "upgradeID"))
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	upgrade, err := controller.InferenceModelUpgradeServiceInterface.GetInferenceModelUpgrade(r.Context(), tenantID, modelID, upgradeID)
	if err != nil {
		writeModelUpgradeError(w, err)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true, Message: "Model upgrade retrieved.", Data: upgrade,
	}
	response.JSON(w)
}

func (controller *InferenceCommandController) CancelInferenceModelUpgrade(w http.ResponseWriter, r *http.Request) {
	if controller.InferenceModelUpgradeServiceInterface == nil {
		writeModelUpgradeUnavailable(w)
		return
	}
	modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
	upgradeID := strings.TrimSpace(chi.URLParam(r, "upgradeID"))
	tenantID, _ := r.Context().Value(iamTypes.TenantIDCtx).(string)
	upgrade, err := controller.InferenceModelUpgradeServiceInterface.CancelInferenceModelUpgrade(r.Context(), tenantID, modelID, upgradeID)
	if err != nil {
		writeModelUpgradeError(w, err)
		return
	}
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusAccepted, Success: true, Message: "Model upgrade cancellation requested.", Data: upgrade,
	}
	response.JSON(w)
}

func writeModelUpgradeUnavailable(w http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusServiceUnavailable, Success: false,
		Message: "Managed model upgrades are unavailable.", ErrorCode: apiError.InferenceUpgradeFailed,
	}
	response.JSON(w)
}

func writeModelUpgradeError(w http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	errorCode := apiError.InferenceUpgradeFailed
	message := "The model upgrade operation failed."
	var typed interface {
		HTTPStatus() int
		ErrorCode() string
	}
	if errors.As(err, &typed) {
		statusCode = typed.HTTPStatus()
		errorCode = typed.ErrorCode()
		message = err.Error()
	} else if errorsIsUpgradeConflict(err) {
		statusCode = http.StatusConflict
		errorCode = apiError.InferenceUpgradeConflict
		message = "A model upgrade is already active."
	}
	response := viewmodels.HTTPResponseVM{
		Status: statusCode, Success: false, Message: message, ErrorCode: errorCode,
	}
	response.JSON(w)
}

func errorsIsUpgradeConflict(err error) bool {
	return errors.Is(err, modelupgrade.ErrConflict)
}
