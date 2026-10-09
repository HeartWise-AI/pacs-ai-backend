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
	info  dockerTypes.GetContainerInfoResult
	infos map[string]dockerTypes.GetContainerInfoResult
	errs  map[string]error
	err   error
	ref   string
	refs  []string
}

func (sdk *managedGatewayDockerSDK) GetContainerInfo(_ context.Context, ref string) (dockerTypes.GetContainerInfoResult, error) {
	sdk.ref = ref
	sdk.refs = append(sdk.refs, ref)
	if err, exists := sdk.errs[ref]; exists {
		return dockerTypes.GetContainerInfoResult{}, err
	}
	if info, exists := sdk.infos[ref]; exists {
		return info, nil
	}
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
	predict     func()
}

func (predictor *recordingManagedPredictor) Predict(_ context.Context, containerID string, request dockerInferenceTypes.PredictRequest) (dockerInferenceTypes.PredictResponse, error) {
	predictor.calls++
	predictor.containerID = containerID
	predictor.request = request
	if predictor.predict != nil {
		predictor.predict()
	}
	return predictor.response, predictor.err
}

type managedGatewayLease struct {
	resolvedID      string
	resolvedVersion string
	tenantID        string
	containerID     string
	modelVersion    string
	released        bool
}

func (lease *managedGatewayLease) AcquireInferenceIngestionTarget(
	_ context.Context, tenantID, containerID, modelVersion string,
) (string, string, func(), error) {
	lease.tenantID = tenantID
	lease.containerID = containerID
	lease.modelVersion = modelVersion
	return lease.resolvedID, lease.resolvedVersion, func() { lease.released = true }, nil
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

func TestPreparedInferenceRedirectsQueuedOldTargetAndHoldsLeaseThroughPrediction(t *testing.T) {
	oldID := strings.Repeat("d", 64)
	newID := strings.Repeat("e", 64)
	sdk := &managedGatewayDockerSDK{infos: map[string]dockerTypes.GetContainerInfoResult{
		"old-model": {ID: oldID, Name: "/old-model"},
		newID:       {ID: newID, Name: "/new-model"},
	}}
	lease := &managedGatewayLease{resolvedID: newID, resolvedVersion: "2.0.0"}
	repository := &managedGatewayRepository{model: entity.InferenceModel{
		TenantID: "tenant-a", ContainerID: newID,
	}}
	predictor := &recordingManagedPredictor{response: dockerInferenceTypes.PredictResponse{Success: true}}
	predictor.predict = func() {
		require.False(t, lease.released, "target lease was released before inference completed")
	}
	service := &InferenceCommandService{
		DockerSDKInterface:                sdk,
		InferenceQueryRepositoryInterface: repository,
		IngestionTargetLeaseRepository:    lease,
		ModelManager:                      predictor,
	}

	response, err := service.PredictPreparedInferenceModel(
		context.Background(), "tenant-a", "old-model", dockerInferenceTypes.PredictRequest{},
	)

	require.NoError(t, err)
	require.True(t, response.Success)
	require.Equal(t, []string{"old-model", newID}, sdk.refs)
	require.Equal(t, "tenant-a", lease.tenantID)
	require.Equal(t, oldID, lease.containerID)
	require.Empty(t, lease.modelVersion)
	require.Equal(t, newID, repository.container)
	require.Equal(t, newID, predictor.containerID)
	require.True(t, lease.released)
}

func TestPreparedInferenceResolvesStableNameAfterOldContainerCleanup(t *testing.T) {
	newID := strings.Repeat("f", 64)
	sdk := &managedGatewayDockerSDK{
		infos: map[string]dockerTypes.GetContainerInfoResult{
			newID: {ID: newID, Name: "/pacs-ai-upgrade-current"},
		},
		errs: map[string]error{"stable-model": errors.New("old named container was removed")},
	}
	lease := &managedGatewayLease{resolvedID: newID, resolvedVersion: "2.0.0"}
	repository := &managedGatewayRepository{model: entity.InferenceModel{
		TenantID: "tenant-a", ContainerID: newID,
	}}
	predictor := &recordingManagedPredictor{response: dockerInferenceTypes.PredictResponse{Success: true}}
	service := &InferenceCommandService{
		DockerSDKInterface:                sdk,
		InferenceQueryRepositoryInterface: repository,
		IngestionTargetLeaseRepository:    lease,
		ModelManager:                      predictor,
	}

	response, err := service.PredictPreparedInferenceModel(
		context.Background(), "tenant-a", "stable-model", dockerInferenceTypes.PredictRequest{},
	)

	require.NoError(t, err)
	require.True(t, response.Success)
	require.Equal(t, []string{"stable-model", newID}, sdk.refs)
	require.Equal(t, "stable-model", lease.containerID)
	require.Equal(t, newID, repository.container)
	require.Equal(t, newID, predictor.containerID)
	require.True(t, lease.released)
}
