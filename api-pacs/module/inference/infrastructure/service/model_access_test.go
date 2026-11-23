package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/service/types"
)

func TestResolveModelArtifactIDs(t *testing.T) {
	models := []entity.InferenceModel{
		{ID: "reg-echo", TenantID: "t1", ContainerID: "c-echo", DockerImage: "img-echo",
			Deployment: &entity.InferenceModelDeployment{ContainerID: "c-echo", ImageReference: "img-echo", ArtifactModelID: "PanEcho"}},
		{ID: "reg-rv", TenantID: "t1", ContainerID: "c-rv"},
		{ID: "reg-old", TenantID: "t1", ContainerID: "c-old"},
		{ID: "reg-foreign", TenantID: "t2", ContainerID: "c-foreign"},
		{ID: "reg-stopped", TenantID: "t1"},
	}
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{models: models}}
	service.rememberModelInfo(context.Background(), types.GetInferenceAvailableModelResult{ContainerID: "c-rv", ModelID: "DeepRV_CLIP"})
	ctx := context.Background()

	for ref, want := range map[string]string{
		"reg-echo": "PanEcho", "c-echo": "PanEcho", "PanEcho": "PanEcho",
		"reg-rv": "DeepRV_CLIP", "c-rv": "DeepRV_CLIP", "DeepRV_CLIP": "DeepRV_CLIP",
	} {
		ids, err := service.ResolveModelArtifactIDs(ctx, "t1", ref)
		require.NoError(t, err, ref)
		require.Equal(t, []string{want}, ids, ref)
	}

	_, err := service.ResolveModelArtifactIDs(ctx, "t1", "reg-old")
	require.EqualError(t, err, apiError.AccessUnavailable, "a model whose modelId is unknown is never allowed by guess")
	_, err = service.ResolveModelArtifactIDs(ctx, "t1", "reg-foreign")
	require.EqualError(t, err, apiError.MissingRecord)
	_, err = service.ResolveModelArtifactIDs(ctx, "t1", "reg-stopped")
	require.EqualError(t, err, apiError.MissingRecord)
	_, err = service.ResolveModelArtifactIDs(ctx, "t1", " ")
	require.EqualError(t, err, apiError.InvalidPayload)

	failing := &InferenceQueryService{InferenceQueryRepositoryInterface: &inferenceModelsQueryRepository{err: errors.New(apiError.FirestoreError)}}
	_, err = failing.ResolveModelArtifactIDs(ctx, "t1", "reg-echo")
	require.EqualError(t, err, apiError.AccessUnavailable)
}

type resultModelsRepository struct {
	inferenceModelsQueryRepository
}

func (*resultModelsRepository) SelectInferenceIngestionJobs(*string) ([]entity.InferenceIngestionJob, error) {
	return nil, nil
}

func TestAuthorizeResultModel(t *testing.T) {
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: &resultModelsRepository{inferenceModelsQueryRepository{models: []entity.InferenceModel{
		{ID: "reg-echo", TenantID: "t1", Name: "PanEcho", ContainerID: "c-echo", DockerImage: "img",
			Deployment: &entity.InferenceModelDeployment{ContainerID: "c-echo", ImageReference: "img", ArtifactModelID: "PanEcho"}},
		{ID: "reg-legacy", TenantID: "t1", Name: "Legacy", ContainerID: "c-legacy"},
	}}}}
	ctx := context.Background()
	onlyEcho := func(id string) bool { return id == "PanEcho" }
	onlyRV := func(id string) bool { return id == "DeepRV_CLIP" }

	require.NoError(t, service.authorizeResultModel(ctx, "t1", "PanEcho", "", nil), "nil allow accepts everything")
	require.NoError(t, service.authorizeResultModel(ctx, "t1", "PanEcho", "1.0.3", onlyEcho))
	require.EqualError(t, service.authorizeResultModel(ctx, "t1", "PanEcho", "", onlyRV), apiError.ForbiddenAccess)
	require.EqualError(t, service.authorizeResultModel(ctx, "t1", "Legacy", "", onlyEcho), apiError.ForbiddenAccess, "unknown modelId is refused")
	require.EqualError(t, service.authorizeResultModel(ctx, "t1", "Unregistered", "", onlyEcho), apiError.ForbiddenAccess)
}
