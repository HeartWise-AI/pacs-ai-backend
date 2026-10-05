package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
)

type managedGatewayDockerSDK struct {
	dockerTypes.DockerSDKInterface
	info dockerTypes.GetContainerInfoResult
	err  error
	ref  string
}

func (sdk *managedGatewayDockerSDK) GetContainerInfo(_ context.Context, ref string) (dockerTypes.GetContainerInfoResult, error) {
	sdk.ref = ref
	return sdk.info, sdk.err
}

type managedGatewayRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	model     entity.InferenceModel
	err       error
	tenantID  string
	container string
}

func (repository *managedGatewayRepository) SelectInferenceModelByContainer(_ context.Context, tenantID, containerID string) (entity.InferenceModel, error) {
	repository.tenantID = tenantID
	repository.container = containerID
	return repository.model, repository.err
}

type recordingManagedPredictor struct {
	containerID string
	request     dockerInferenceTypes.PredictRequest
	response    dockerInferenceTypes.PredictResponse
	err         error
	calls       int
}

func (predictor *recordingManagedPredictor) Predict(_ context.Context, containerID string, request dockerInferenceTypes.PredictRequest) (dockerInferenceTypes.PredictResponse, error) {
	predictor.calls++
	predictor.containerID = containerID
	predictor.request = request
	return predictor.response, predictor.err
}

func TestPreparedInferenceUsesSharedManagerAfterTenantAuthorization(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	sdk := &managedGatewayDockerSDK{info: dockerTypes.GetContainerInfoResult{ID: containerID, Name: "/echo-prime"}}
	repository := &managedGatewayRepository{model: entity.InferenceModel{TenantID: "tenant-a", ContainerID: containerID}}
	predictor := &recordingManagedPredictor{response: dockerInferenceTypes.PredictResponse{Success: true}}
	direct := &quotaDockerInferenceAPI{}
	service := &InferenceCommandService{
		DockerSDKInterface:                sdk,
		InferenceQueryRepositoryInterface: repository,
		DockerInferenceAPIInterface:       direct,
		ModelManager:                      predictor,
	}
	request := dockerInferenceTypes.PredictRequest{OutputMode: dockerInferenceTypes.OutputModeJSON}

	response, err := service.PredictPreparedInferenceModel(context.Background(), " tenant-a ", " echo-prime ", request)

	require.NoError(t, err)
	require.True(t, response.Success)
	require.Equal(t, "echo-prime", sdk.ref)
	require.Equal(t, "tenant-a", repository.tenantID)
	require.Equal(t, containerID, repository.container)
	require.Equal(t, containerID, predictor.containerID)
	require.Equal(t, request, predictor.request)
	require.Equal(t, 1, predictor.calls)
	require.Zero(t, direct.calls)
}

func TestPreparedInferenceFallsBackToDirectProviderWhenManagerDisabled(t *testing.T) {
	containerID := strings.Repeat("b", 64)
	direct := &quotaDockerInferenceAPI{response: dockerInferenceTypes.PredictResponse{Success: true}}
	service := &InferenceCommandService{
		DockerSDKInterface: &managedGatewayDockerSDK{info: dockerTypes.GetContainerInfoResult{
			ID: containerID, Name: "/ct-model",
		}},
		InferenceQueryRepositoryInterface: &managedGatewayRepository{model: entity.InferenceModel{
			TenantID: "tenant-a", ContainerID: containerID,
		}},
		DockerInferenceAPIInterface: direct,
	}

	response, err := service.PredictPreparedInferenceModel(
		context.Background(), "tenant-a", "ct-model", dockerInferenceTypes.PredictRequest{},
	)

	require.NoError(t, err)
	require.True(t, response.Success)
	require.Equal(t, 1, direct.calls)
}

func TestPreparedInferenceRejectsUnregisteredTenantBeforePrediction(t *testing.T) {
	containerID := strings.Repeat("c", 64)
	predictor := &recordingManagedPredictor{}
	service := &InferenceCommandService{
		DockerSDKInterface: &managedGatewayDockerSDK{info: dockerTypes.GetContainerInfoResult{
			ID: containerID, Name: "/private-model",
		}},
		InferenceQueryRepositoryInterface: &managedGatewayRepository{err: errors.New(apiError.MissingRecord)},
		ModelManager:                      predictor,
	}

	_, err := service.PredictPreparedInferenceModel(
		context.Background(), "tenant-b", "private-model", dockerInferenceTypes.PredictRequest{},
	)

	require.EqualError(t, err, apiError.MissingRecord)
	require.Zero(t, predictor.calls)
}
