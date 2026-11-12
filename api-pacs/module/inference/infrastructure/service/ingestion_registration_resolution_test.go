package service

import (
	"context"
	"errors"
	"testing"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
	repository "api-pacs/module/inference/domain/repository"
	"github.com/stretchr/testify/require"
)

type ingestionRegistrationReader struct {
	repository.InferenceQueryRepositoryInterface
	model entity.InferenceModel
	err   error
}

func (reader *ingestionRegistrationReader) SelectInferenceModelByID(context.Context, string) (entity.InferenceModel, error) {
	return reader.model, reader.err
}

type ingestionContainerCapture struct {
	guardedDispatchDockerSDK
	target string
}

func (sdk *ingestionContainerCapture) GetContainerInfo(ctx context.Context, id string) (dockerTypes.GetContainerInfoResult, error) {
	sdk.target = id
	return sdk.guardedDispatchDockerSDK.GetContainerInfo(ctx, id)
}

func TestIngestionResolvesCurrentRegistrationWithoutRewritingHistory(t *testing.T) {
	job := entity.InferenceIngestionJob{ID: "job", TenantID: "tenant", ModelID: "registration", ContainerID: "old-container", ModelName: "model", ModelVersion: "1.0.0"}
	service := &InferenceCommandService{InferenceQueryRepositoryInterface: &ingestionRegistrationReader{model: entity.InferenceModel{TenantID: "tenant", ContainerID: "new-container", Deployment: &entity.InferenceModelDeployment{ModelVersion: "1.0.0"}}}}
	resolved, err := service.resolveCurrentIngestionModel(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, "new-container", resolved.ContainerID)
	require.Equal(t, "old-container", job.ContainerID)
	require.Equal(t, job.ModelVersion, resolved.ModelVersion)
	require.Equal(t, job.ModelName, resolved.ModelName)
	sdk := &ingestionContainerCapture{}
	service.DockerSDKInterface = sdk
	service.DockerInferenceAPIInterface = &guardedDispatchDockerInferenceAPI{}
	require.NoError(t, service.ensureInferenceContainerReady(context.Background(), job))
	require.Equal(t, "new-container", sdk.target)
}

func TestIngestionRegistrationResolutionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model entity.InferenceModel
		err   error
	}{
		{name: "wrong tenant", model: entity.InferenceModel{TenantID: "other", ContainerID: "new-container"}},
		{name: "empty container", model: entity.InferenceModel{TenantID: "tenant"}},
		{name: "missing registration", err: errors.New("missing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sdk := &guardedDispatchDockerSDK{}
			service := &InferenceCommandService{InferenceQueryRepositoryInterface: &ingestionRegistrationReader{model: tc.model, err: tc.err}, DockerSDKInterface: sdk, DockerInferenceAPIInterface: &guardedDispatchDockerInferenceAPI{}}
			err := service.ensureInferenceContainerReady(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "registration", ContainerID: "old-container"})
			require.Error(t, err)
			require.Zero(t, sdk.inspectCalls)
		})
	}
}

func TestIngestionDoesNotRunDifferentVersionUnderHistoricalJobVersion(t *testing.T) {
	service := &InferenceCommandService{InferenceQueryRepositoryInterface: &ingestionRegistrationReader{model: entity.InferenceModel{TenantID: "tenant", ContainerID: "new", Deployment: &entity.InferenceModelDeployment{ModelVersion: "2.0.0"}}}}
	_, err := service.resolveCurrentIngestionModel(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "registration", ContainerID: "old", ModelVersion: "1.0.0"})
	require.ErrorContains(t, err, "model version changed")
}
