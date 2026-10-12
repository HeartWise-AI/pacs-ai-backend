package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type inferenceModelsQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	models []entity.InferenceModel
	err    error
}

func (repository *inferenceModelsQueryRepository) SelectInferenceModels(_ context.Context, _ string) ([]entity.InferenceModel, error) {
	return repository.models, repository.err
}

type inferenceModelsDockerSDK struct {
	dockerTypes.DockerSDKInterface

	mu        sync.Mutex
	infos     map[string]dockerTypes.GetContainerInfoResult
	errors    map[string]error
	started   chan string
	release   <-chan struct{}
	active    int
	maxActive int
}

func (sdk *inferenceModelsDockerSDK) GetContainerInfo(ctx context.Context, containerID string) (dockerTypes.GetContainerInfoResult, error) {
	sdk.mu.Lock()
	sdk.active++
	if sdk.active > sdk.maxActive {
		sdk.maxActive = sdk.active
	}
	sdk.mu.Unlock()

	if sdk.started != nil {
		sdk.started <- containerID
	}
	if sdk.release != nil {
		select {
		case <-ctx.Done():
			sdk.finishInspection()
			return dockerTypes.GetContainerInfoResult{}, ctx.Err()
		case <-sdk.release:
		}
	}

	sdk.finishInspection()
	if err := sdk.errors[containerID]; err != nil {
		return dockerTypes.GetContainerInfoResult{}, err
	}
	return sdk.infos[containerID], nil
}

func (sdk *inferenceModelsDockerSDK) finishInspection() {
	sdk.mu.Lock()
	sdk.active--
	sdk.mu.Unlock()
}

func (sdk *inferenceModelsDockerSDK) maximumActive() int {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	return sdk.maxActive
}

type inferenceModelsDockerInferenceAPI struct {
	dockerInferenceTypes.DockerInferenceAPIInterface

	mu         sync.Mutex
	started    chan string
	release    <-chan struct{}
	active     int
	maxActive  int
	infoCalls  int
	factsCalls int
}

func (api *inferenceModelsDockerInferenceAPI) GetModelInfo(ctx context.Context, containerName string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	api.mu.Lock()
	api.active++
	api.infoCalls++
	if api.active > api.maxActive {
		api.maxActive = api.active
	}
	api.mu.Unlock()

	if api.started != nil {
		api.started <- containerName
	}
	if api.release != nil {
		select {
		case <-ctx.Done():
			api.finishModelInfo()
			return dockerInferenceTypes.GetModelInfoResponse{}, ctx.Err()
		case <-api.release:
		}
	}
	api.finishModelInfo()

	return dockerInferenceTypes.GetModelInfoResponse{
		Success: true,
		Data: dockerInferenceTypes.ModelInfo{
			ModelID:   containerName + "-id",
			ModelName: containerName,
			Version:   "1.0.0",
		},
	}, nil
}

func (api *inferenceModelsDockerInferenceAPI) GetModelFacts(context.Context, string) (dockerInferenceTypes.GetModelFactsResponse, error) {
	api.mu.Lock()
	api.factsCalls++
	api.mu.Unlock()

	response := dockerInferenceTypes.GetModelFactsResponse{Success: true}
	response.Data.En = map[string]interface{}{"summary": "facts"}
	return response, nil
}

func (api *inferenceModelsDockerInferenceAPI) finishModelInfo() {
	api.mu.Lock()
	api.active--
	api.mu.Unlock()
}

func (api *inferenceModelsDockerInferenceAPI) snapshot() (maxActive, infoCalls, factsCalls int) {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.maxActive, api.infoCalls, api.factsCalls
}

func TestGetInferenceModelsUsesBoundedConcurrentInspection(t *testing.T) {
	models := make([]entity.InferenceModel, 6)
	infos := make(map[string]dockerTypes.GetContainerInfoResult, len(models))
	for index := range models {
		containerID := fmt.Sprintf("container-%d", index)
		models[index] = entity.InferenceModel{
			ID: containerID, TenantID: "tenant-a", ContainerID: containerID, Name: fmt.Sprintf("model-%d", index),
		}
		infos[containerID] = dockerTypes.GetContainerInfoResult{
			ID: containerID, Name: "/" + models[index].Name, Lifecycle: dockerTypes.LifecycleRunning,
		}
	}

	release := make(chan struct{})
	sdk := &inferenceModelsDockerSDK{
		infos: infos, errors: map[string]error{}, started: make(chan string, len(models)), release: release,
	}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{models: models},
		DockerSDKInterface:                sdk,
	}

	type response struct {
		models []serviceTypes.GetInferenceModelResult
		err    error
	}
	responseChannel := make(chan response, 1)
	go func() {
		result, err := service.GetInferenceModels(context.Background(), "tenant-a")
		responseChannel <- response{models: result, err: err}
	}()

	for index := 0; index < inferenceModelInspectionConcurrency; index++ {
		select {
		case <-sdk.started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent Docker inspections")
		}
	}
	select {
	case unexpected := <-sdk.started:
		t.Fatalf("inspection concurrency exceeded limit: %s started early", unexpected)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	result := <-responseChannel
	require.NoError(t, result.err)
	require.Len(t, result.models, len(models))
	for index, model := range result.models {
		require.Equal(t, models[index].ID, model.ID)
		require.Equal(t, models[index].ContainerID, model.Container.ID)
		require.Equal(t, serviceTypes.InferenceModelLifecycleRunning, model.Container.Lifecycle)
	}
	require.Equal(t, inferenceModelInspectionConcurrency, sdk.maximumActive())
}

func TestGetInferenceModelsKeepsRegistrationWhenContainerInspectionFails(t *testing.T) {
	model := entity.InferenceModel{
		ID: "model-1", TenantID: "tenant-a", ContainerID: "missing-container", Name: "registered-model",
	}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{models: []entity.InferenceModel{model}},
		DockerSDKInterface: &inferenceModelsDockerSDK{
			infos:  map[string]dockerTypes.GetContainerInfoResult{},
			errors: map[string]error{"missing-container": errors.New("container not found")},
		},
	}

	result, err := service.GetInferenceModels(context.Background(), "tenant-a")

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, model.ID, result[0].ID)
	require.Equal(t, model.ContainerID, result[0].Container.ID)
	require.Equal(t, serviceTypes.InferenceModelLifecycleUnknown, result[0].Container.Lifecycle)
}

func TestGetInferenceModelsPropagatesRequestCancellation(t *testing.T) {
	models := make([]entity.InferenceModel, inferenceModelInspectionConcurrency+1)
	for index := range models {
		containerID := fmt.Sprintf("container-%d", index)
		models[index] = entity.InferenceModel{ID: containerID, ContainerID: containerID}
	}

	sdk := &inferenceModelsDockerSDK{
		infos:   map[string]dockerTypes.GetContainerInfoResult{},
		errors:  map[string]error{},
		started: make(chan string, len(models)),
		release: make(chan struct{}),
	}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{models: models},
		DockerSDKInterface:                sdk,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := service.GetInferenceModels(ctx, "tenant-a")
		done <- err
	}()

	select {
	case <-sdk.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Docker inspection")
	}
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled model list did not return")
	}
}

func TestGetInferenceAvailableModelsUsesBoundedConcurrentModelInfoChecks(t *testing.T) {
	models := make([]entity.InferenceModel, 6)
	infos := make(map[string]dockerTypes.GetContainerInfoResult, len(models))
	for index := range models {
		containerID := fmt.Sprintf("container-%d", index)
		containerName := fmt.Sprintf("model-%d", index)
		models[index] = entity.InferenceModel{
			ID: containerID, TenantID: "tenant-a", ContainerID: containerID, Name: containerName,
		}
		infos[containerID] = dockerTypes.GetContainerInfoResult{
			ID: containerID, Name: "/" + containerName, Lifecycle: dockerTypes.LifecycleRunning,
		}
	}

	release := make(chan struct{})
	api := &inferenceModelsDockerInferenceAPI{
		started: make(chan string, len(models)), release: release,
	}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{models: models},
		DockerSDKInterface: &inferenceModelsDockerSDK{
			infos: infos, errors: map[string]error{},
		},
		DockerInferenceAPIInterface: api,
	}

	type response struct {
		models []serviceTypes.GetInferenceAvailableModelResult
		err    error
	}
	responseChannel := make(chan response, 1)
	go func() {
		result, err := service.GetInferenceAvailableModels(context.Background(), "tenant-a")
		responseChannel <- response{models: result, err: err}
	}()

	for index := 0; index < inferenceModelInspectionConcurrency; index++ {
		select {
		case <-api.started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent model-info requests")
		}
	}
	select {
	case unexpected := <-api.started:
		t.Fatalf("model-info concurrency exceeded limit: %s started early", unexpected)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	result := <-responseChannel
	require.NoError(t, result.err)
	require.Len(t, result.models, len(models))
	for index, model := range result.models {
		require.Equal(t, models[index].ContainerID, model.ContainerID)
		require.Equal(t, models[index].Name, model.ContainerName)
		require.Equal(t, models[index].Name+"-id", model.ModelID)
	}
	maxActive, infoCalls, factsCalls := api.snapshot()
	require.Equal(t, inferenceModelInspectionConcurrency, maxActive)
	require.Equal(t, len(models), infoCalls)
	require.Equal(t, len(models), factsCalls)
}

func TestGetInferenceAvailableModelsPropagatesRequestCancellation(t *testing.T) {
	model := entity.InferenceModel{ID: "model-1", ContainerID: "container-1"}
	api := &inferenceModelsDockerInferenceAPI{
		started: make(chan string, 1), release: make(chan struct{}),
	}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{models: []entity.InferenceModel{model}},
		DockerSDKInterface: &inferenceModelsDockerSDK{
			infos: map[string]dockerTypes.GetContainerInfoResult{
				model.ContainerID: {ID: model.ContainerID, Name: "/model-1", Lifecycle: dockerTypes.LifecycleRunning},
			},
			errors: map[string]error{},
		},
		DockerInferenceAPIInterface: api,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := service.GetInferenceAvailableModels(ctx, "tenant-a")
		done <- err
	}()

	select {
	case <-api.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for model-info request")
	}
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled available-model list did not return")
	}
}
