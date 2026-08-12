package rest

import (
	stderrors "errors"
	"net/http"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

func writeInferenceInputError(writer http.ResponseWriter, err error) bool {
	var inputError *apiError.InferenceModelInputError
	if stderrors.As(err, &inputError) {
		message := "Inference input violates the configured safety limits."
		if inputError.ErrorCode == apiError.InferenceModelInputOutOfRange {
			message = "Selected series count is outside the model input requirements."
		}
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   message,
			ErrorCode: inputError.ErrorCode,
			Data: map[string]int{
				"minimum": inputError.Minimum,
				"maximum": inputError.Maximum,
				"actual":  inputError.Actual,
			},
		}
		response.JSON(writer)
		return true
	}

	switch err.Error() {
	case apiError.InferenceInputInvalid:
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Inference input is invalid.",
			ErrorCode: apiError.InferenceInputInvalid,
		}
		response.JSON(writer)
		return true
	case apiError.InferenceModelConfigurationInvalid:
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusServiceUnavailable,
			Success:   false,
			Message:   "The selected model input configuration is unavailable.",
			ErrorCode: apiError.InferenceModelConfigurationInvalid,
		}
		response.JSON(writer)
		return true
	default:
		return false
	}
}
