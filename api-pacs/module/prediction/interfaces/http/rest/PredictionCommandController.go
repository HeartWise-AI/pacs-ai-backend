package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/prediction/application"
	types "api-pacs/module/prediction/interfaces/http"
)

type PredictionCommandController struct {
	application.PredictionCommandServiceInterface
}

// Predict apply prediction to selected query id
func (controller *PredictionCommandController) Predict(w http.ResponseWriter, r *http.Request) {
	var request types.PredictionRequest

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

	res, err := controller.PredictionCommandServiceInterface.Predict(context.TODO(), request.QueryID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DICOMParseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Unexpected error while parsing DICOM instance."
		case apiError.InferenceError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Unexpected result from inference."
		case apiError.TorchServeError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Inference service encountered an error."
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
		Message: "Successfully applied prediction.",
		Data: &types.PredictionResponse{
			Vessel: res.Vessel,
			LVEF:   res.LVEF,
			Age:    res.Age,
		},
	}

	response.JSON(w)
}
