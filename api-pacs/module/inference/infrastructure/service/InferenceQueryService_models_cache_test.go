package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

// busyModelAPI answers model info and facts unless the container is marked busy.
type busyModelAPI struct {
	dockerInferenceTypes.DockerInferenceAPIInterface
	busyInfo  map[string]bool
	busyFacts map[string]bool
}

func (api *busyModelAPI) GetModelInfo(_ context.Context, containerName string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	if api.busyInfo[containerName] {
		return dockerInferenceTypes.GetModelInfoResponse{}, errors.New("timeout")
	}
	return dockerInferenceTypes.GetModelInfoResponse{
		Success: true,
		Data:    dockerInferenceTypes.ModelInfo{ModelID: containerName + "-id", ModelName: containerName, Version: "1.0.0"},
	}, nil
}

func (api *busyModelAPI) GetModelFacts(_ context.Context, containerName string) (dockerInferenceTypes.GetModelFactsResponse, error) {
	if api.busyFacts[containerName] {
		return dockerInferenceTypes.GetModelFactsResponse{}, errors.New("timeout")
	}
	response := dockerInferenceTypes.GetModelFactsResponse{Success: true}
	response.Data.En = map[string]interface{}{"summary": containerName}
	return response, nil
}

func newBusyModelFixture() (*InferenceQueryService, *inferenceModelsQueryRepository, *inferenceModelsDockerSDK, *busyModelAPI) {
	models := []entity.InferenceModel{
		{ID: "a", ContainerID: "container-a", Name: "panecho", OutputMode: entity.OutputModeJSON},
		{ID: "b", ContainerID: "container-b", Name: "jepa", OutputMode: entity.OutputModeJSON},
	}
	repository := &inferenceModelsQueryRepository{models: models}
	sdk := &inferenceModelsDockerSDK{
		infos: map[string]dockerTypes.GetContainerInfoResult{
			"container-a": {ID: "container-a", Name: "/panecho", Lifecycle: dockerTypes.LifecycleRunning},
			"container-b": {ID: "container-b", Name: "/jepa", Lifecycle: dockerTypes.LifecycleRunning},
		},
		errors: map[string]error{},
	}
	api := &busyModelAPI{busyInfo: map[string]bool{}, busyFacts: map[string]bool{}}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: repository,
		DockerSDKInterface:                sdk,
		DockerInferenceAPIInterface:       api,
	}
	return service, repository, sdk, api
}

func modelIDs(t *testing.T, service *InferenceQueryService) []string {
	t.Helper()
	result, err := service.GetInferenceAvailableModels(context.Background(), "tenant-a")
	require.NoError(t, err)
	ids := make([]string, 0, len(result))
	for _, model := range result {
		ids = append(ids, model.ModelID)
	}
	return ids
}

func TestGetInferenceAvailableModelsKeepsBusyModelFromLastAnswer(t *testing.T) {
	service, repository, _, api := newBusyModelFixture()
	require.Equal(t, []string{"panecho-id", "jepa-id"}, modelIDs(t, service))

	api.busyInfo["panecho"] = true
	repository.models[0].OutputMode = entity.OutputModeHTML
	result, err := service.GetInferenceAvailableModels(context.Background(), "tenant-a")
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "panecho-id", result[0].ModelID)
	require.Equal(t, "panecho", result[0].ContainerName)
	require.Equal(t, "panecho", result[0].ModelFacts.En["summary"])
	require.Equal(t, entity.OutputModeHTML, result[0].OutputMode, "output mode comes from the database, not the cache")
}

func TestGetInferenceAvailableModelsKeepsModelWhenOnlyFactsTimeOut(t *testing.T) {
	service, _, _, api := newBusyModelFixture()
	modelIDs(t, service)

	api.busyFacts["jepa"] = true
	require.Equal(t, []string{"panecho-id", "jepa-id"}, modelIDs(t, service))
}

func TestGetInferenceAvailableModelsDropsBusyModelNeverSeen(t *testing.T) {
	service, _, _, api := newBusyModelFixture()
	api.busyInfo["panecho"] = true
	require.Equal(t, []string{"jepa-id"}, modelIDs(t, service))
}

func TestGetInferenceAvailableModelsDropsStoppedModelEvenIfCached(t *testing.T) {
	service, _, sdk, _ := newBusyModelFixture()
	modelIDs(t, service)

	stopped := sdk.infos["container-a"]
	stopped.Lifecycle = dockerTypes.LifecycleStopped
	sdk.infos["container-a"] = stopped
	require.Equal(t, []string{"jepa-id"}, modelIDs(t, service))
}

func TestGetInferenceAvailableModelsIgnoresCacheOfRecreatedContainer(t *testing.T) {
	service, repository, sdk, api := newBusyModelFixture()
	modelIDs(t, service)

	repository.models[0].ContainerID = "container-a2"
	sdk.infos["container-a2"] = dockerTypes.GetContainerInfoResult{ID: "container-a2", Name: "/panecho", Lifecycle: dockerTypes.LifecycleRunning}
	api.busyInfo["panecho"] = true
	require.Equal(t, []string{"jepa-id"}, modelIDs(t, service))
}
