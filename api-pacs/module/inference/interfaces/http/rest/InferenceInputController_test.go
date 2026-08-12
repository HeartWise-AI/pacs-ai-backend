package rest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
)

func TestWriteInferenceInputErrorReturnsModelBounds(t *testing.T) {
	recorder := httptest.NewRecorder()

	handled := writeInferenceInputError(recorder, &apiError.InferenceModelInputError{
		ErrorCode: apiError.InferenceModelInputOutOfRange,
		Minimum:   2,
		Maximum:   4,
		Actual:    5,
	})

	require.True(t, handled)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.JSONEq(t, `{
		"success": false,
		"message": "Selected series count is outside the model input requirements.",
		"errorCode": "INFERENCE_MODEL_INPUT_OUT_OF_RANGE",
		"data": {"minimum": 2, "maximum": 4, "actual": 5}
	}`, recorder.Body.String())
}

func TestWriteInferenceInputErrorMapsInvalidAndConfigurationErrors(t *testing.T) {
	testCases := []struct {
		errorCode string
		status    int
	}{
		{errorCode: apiError.InferenceInputInvalid, status: http.StatusBadRequest},
		{errorCode: apiError.InferenceModelConfigurationInvalid, status: http.StatusServiceUnavailable},
	}
	for _, testCase := range testCases {
		t.Run(testCase.errorCode, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			require.True(t, writeInferenceInputError(recorder, errors.New(testCase.errorCode)))
			require.Equal(t, testCase.status, recorder.Code)
		})
	}
}
