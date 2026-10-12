package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	responseTypes "api-pacs/module/inference/interfaces/http"
)

type inferenceModelsQueryService struct {
	application.InferenceQueryServiceInterface
	models []serviceTypes.GetInferenceModelResult
}

func (service *inferenceModelsQueryService) GetInferenceModels(context.Context, string) ([]serviceTypes.GetInferenceModelResult, error) {
	return service.models, nil
}

func TestGetInferenceModelsSerializesUnavailableContainerTimestampsAsZero(t *testing.T) {
	controller := &InferenceQueryController{
		InferenceQueryServiceInterface: &inferenceModelsQueryService{
			models: []serviceTypes.GetInferenceModelResult{{
				ID: "model-1",
				Container: serviceTypes.GetContainerInfoResult{
					ID:        "missing-container",
					Lifecycle: serviceTypes.InferenceModelLifecycleUnknown,
				},
			}},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/inference/model/list", nil)
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a"))
	recorder := httptest.NewRecorder()

	controller.GetInferenceModels(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	body, err := io.ReadAll(recorder.Body)
	require.NoError(t, err)
	var response struct {
		Data []responseTypes.GetInferenceModelResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.Len(t, response.Data, 1)
	require.Zero(t, response.Data[0].Container.StartedAt)
	require.Zero(t, response.Data[0].Container.FinishedAt)
	require.Equal(t, serviceTypes.InferenceModelLifecycleUnknown, response.Data[0].Container.Lifecycle)

	var rawResponse struct {
		Data []struct {
			Container map[string]json.RawMessage `json:"container"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &rawResponse))
	require.NotContains(t, rawResponse.Data[0].Container, "status")
	require.NotContains(t, rawResponse.Data[0].Container, "running")
}
