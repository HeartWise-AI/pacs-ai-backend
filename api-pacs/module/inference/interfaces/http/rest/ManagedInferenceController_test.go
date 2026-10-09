package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/infrastructure/modelmanager"
)

type managedInferenceControllerService struct {
	application.InferenceCommandServiceInterface
	tenantID     string
	containerRef string
	request      dockerInferenceTypes.PredictRequest
	response     dockerInferenceTypes.PredictResponse
	err          error
	calls        int
}

func (service *managedInferenceControllerService) PredictPreparedInferenceModel(
	_ context.Context,
	tenantID string,
	containerRef string,
	request dockerInferenceTypes.PredictRequest,
) (dockerInferenceTypes.PredictResponse, error) {
	service.calls++
	service.tenantID = tenantID
	service.containerRef = containerRef
	service.request = request
	return service.response, service.err
}

func managedInferenceRequest(body string, token string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/inference/predict", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func TestManagedInferenceGatewayAuthenticatesAndForwardsPreparedRequest(t *testing.T) {
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "gateway-secret")
	service := &managedInferenceControllerService{response: dockerInferenceTypes.PredictResponse{
		Success: true, Data: map[string]interface{}{"ef": float64(55)},
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.PredictPreparedInferenceModel(recorder, managedInferenceRequest(`{
		"tenantId":"tenant-a",
		"containerRef":"echo-prime",
		"request":{"outputMode":"JSON","seriesInstanceImages":{"0":{"0":"YWJj"}}}
	}`, "gateway-secret"))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, service.calls)
	require.Equal(t, "tenant-a", service.tenantID)
	require.Equal(t, "echo-prime", service.containerRef)
	require.Equal(t, dockerInferenceTypes.OutputModeJSON, service.request.OutputMode)
	var response struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, float64(55), response.Data["ef"])
}

func TestManagedInferenceGatewayRejectsInvalidBearerToken(t *testing.T) {
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "gateway-secret")
	service := &managedInferenceControllerService{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.PredictPreparedInferenceModel(recorder, managedInferenceRequest(`{}`, "wrong-secret"))

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, service.calls)
}

func TestManagedInferenceGatewayMapsAdmissionTimeoutToRetryable503(t *testing.T) {
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "gateway-secret")
	service := &managedInferenceControllerService{err: &modelmanager.AdmissionTimeout{}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.PredictPreparedInferenceModel(recorder, managedInferenceRequest(`{
		"tenantId":"tenant-a","containerRef":"echo-prime","request":{"outputMode":"JSON"}
	}`, "gateway-secret"))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, "1", recorder.Header().Get("Retry-After"))
}

func TestManagedInferenceGatewayMapsDeploymentDrainToRetryable503(t *testing.T) {
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "gateway-secret")
	service := &managedInferenceControllerService{err: &modelmanager.AdmissionBlocked{}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.PredictPreparedInferenceModel(recorder, managedInferenceRequest(`{
		"tenantId":"tenant-a","containerRef":"echo-prime","request":{"outputMode":"JSON"}
	}`, "gateway-secret"))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Equal(t, "1", recorder.Header().Get("Retry-After"))
	require.Contains(t, recorder.Body.String(), "INFERENCE_ADMISSION_BLOCKED")
}
