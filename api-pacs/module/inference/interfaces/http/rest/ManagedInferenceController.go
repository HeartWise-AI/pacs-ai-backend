package rest

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/go-playground/validator/v10"

	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	types "api-pacs/module/inference/interfaces/http"
)

const defaultManagedInferenceMaxRequestBodyBytes int64 = 2 * 1024 * 1024 * 1024

// PredictPreparedInferenceModel is the authenticated study-service gateway for
// already prepared DICOM payloads. The service resolves and authorizes the
// container reference before it reaches the model manager.
func (controller *InferenceCommandController) PredictPreparedInferenceModel(w http.ResponseWriter, r *http.Request) {
	if !authorizeStudyService(w, r) {
		return
	}

	var request types.ManagedInferencePredictRequest
	maxBytes := requestbody.PositiveInt64FromEnvironment(
		"INTERNAL_INFERENCE_PREDICT_MAX_REQUEST_BODY_BYTES",
		defaultManagedInferenceMaxRequestBodyBytes,
	)
	if err := requestbody.DecodeJSON(w, r, &request, maxBytes); err != nil {
		if requestbody.IsTooLarge(err) {
			requestbody.ObserveRejection(r, maxBytes, "internal_inference")
			requestbody.WriteTooLarge(w)
			return
		}
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}
	if err := types.Validate.Struct(request); err != nil {
		if _, ok := err.(validator.ValidationErrors); ok {
			writeManagedInferenceResponse(w, http.StatusBadRequest, "Tenant ID and container reference are required.", apiError.InvalidPayload)
			return
		}
		writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}

	result, err := controller.InferenceCommandServiceInterface.PredictPreparedInferenceModel(
		r.Context(), request.TenantID, request.ContainerRef, request.Request,
	)
	if err != nil {
		if writeInferenceManagerError(w, err) {
			return
		}
		switch err.Error() {
		case apiError.InvalidPayload:
			writeManagedInferenceResponse(w, http.StatusBadRequest, "Invalid payload request.", err.Error())
		case apiError.MissingRecord:
			writeManagedInferenceResponse(w, http.StatusNotFound, "Inference model is not registered for this tenant.", err.Error())
		case apiError.DockerError, apiError.DockerInferenceError:
			writeManagedInferenceResponse(w, http.StatusInternalServerError, "Inference runtime encountered an error.", err.Error())
		default:
			writeManagedInferenceResponse(w, http.StatusInternalServerError, "Please contact technical support.", err.Error())
		}
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully applied prediction to inference model.",
		Data:    result.Data,
	}
	response.JSON(w)
}

func authorizeStudyService(w http.ResponseWriter, r *http.Request) bool {
	token := strings.TrimSpace(os.Getenv("STUDY_SERVICE_CALLBACK_TOKEN"))
	if token == "" {
		writeManagedInferenceResponse(w, http.StatusServiceUnavailable, "Study-service gateway auth is not configured.", apiError.MissingConfiguration)
		return false
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, candidate, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(strings.TrimSpace(scheme), "Bearer") ||
		subtle.ConstantTimeCompare([]byte(strings.TrimSpace(candidate)), []byte(token)) != 1 {
		writeManagedInferenceResponse(w, http.StatusUnauthorized, "Invalid Authorization header.", apiError.UnauthorizedAccess)
		return false
	}
	return true
}

func writeManagedInferenceResponse(w http.ResponseWriter, status int, message string, errorCode interface{}) {
	response := viewmodels.HTTPResponseVM{
		Status: status, Success: false, Message: message, ErrorCode: errorCode,
	}
	response.JSON(w)
}
