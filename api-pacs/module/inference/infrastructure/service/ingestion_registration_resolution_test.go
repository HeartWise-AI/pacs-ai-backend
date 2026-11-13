package service

import (
	"context"
	"errors"
	"testing"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
	repository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	"github.com/stretchr/testify/require"
)

type ingestionRegistrationReader struct {
	repository.InferenceQueryRepositoryInterface
	model  entity.InferenceModel
	models []entity.InferenceModel
	err    error
}

func (reader *ingestionRegistrationReader) SelectInferenceModels(context.Context, string) ([]entity.InferenceModel, error) {
	if reader.err != nil {
		return nil, reader.err
	}
	if reader.models != nil {
		return reader.models, nil
	}
	model := reader.model
	if model.ID == "" {
		model.ID = "registration"
	}
	return []entity.InferenceModel{model}, nil
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
	service := &InferenceCommandService{InferenceQueryRepositoryInterface: &ingestionRegistrationReader{model: entity.InferenceModel{TenantID: "tenant", ContainerID: "new-container", Deployment: &entity.InferenceModelDeployment{ContainerID: "new-container", ModelVersion: "1.0.0"}}}}
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
	service := &InferenceCommandService{InferenceQueryRepositoryInterface: &ingestionRegistrationReader{model: entity.InferenceModel{TenantID: "tenant", ContainerID: "new", Deployment: &entity.InferenceModelDeployment{ContainerID: "new", ModelVersion: "2.0.0"}}}}
	_, err := service.resolveCurrentIngestionModel(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "registration", ContainerID: "old", ModelVersion: "1.0.0"})
	require.ErrorContains(t, err, "model version changed")
}

func TestHistoricalLogicalArtifactIDFollowsCurrentRegistration(t *testing.T) {
	job := entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "CathEF_CLIP", ContainerID: "retained-old", ModelVersion: "1.0.1"}
	reader := &ingestionRegistrationReader{model: entity.InferenceModel{ID: "firestore-document", TenantID: "tenant", ContainerID: "current", Deployment: &entity.InferenceModelDeployment{ContainerID: "current", ArtifactModelID: "CathEF_CLIP", ModelVersion: "1.0.1"}}}
	command := &InferenceCommandService{InferenceQueryRepositoryInterface: reader}
	resolved, err := command.resolveCurrentIngestionModel(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, "current", resolved.ContainerID)
	require.Equal(t, "CathEF_CLIP", job.ModelID)
	require.Equal(t, "retained-old", job.ContainerID)
}

type registrationJobCapture struct {
	repository.InferenceCommandRepositoryInterface
	stored repositoryTypes.CreateInferenceIngestionJob
}

func (capture *registrationJobCapture) InsertInferenceIngestionJob(job repositoryTypes.CreateInferenceIngestionJob) error {
	capture.stored = job
	return nil
}

func TestCreateAndImportCanonicalizeLogicalIDThenFollowRegistrationAfterMove(t *testing.T) {
	for _, importing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "import"}[importing], func(t *testing.T) {
			reader := &ingestionRegistrationReader{model: entity.InferenceModel{ID: "firestore-document", TenantID: "tenant", ContainerID: "original", Deployment: &entity.InferenceModelDeployment{ContainerID: "original", ArtifactModelID: "CathEF_CLIP", ModelVersion: "1.0.1"}}}
			capture := &registrationJobCapture{}
			command := &InferenceCommandService{InferenceQueryRepositoryInterface: reader, InferenceCommandRepositoryInterface: capture}
			input := serviceTypes.CreateInferenceIngestionJob{TenantID: "tenant", ModelID: "CathEF_CLIP", ContainerID: "original", ModelVersion: "1.0.1"}
			if importing {
				require.NoError(t, command.ImportInferenceIngestionJobs(context.Background(), []serviceTypes.CreateInferenceIngestionJob{input}))
			} else {
				require.NoError(t, command.CreateInferenceIngestionJob(context.Background(), input))
			}
			require.Equal(t, "firestore-document", capture.stored.ModelID)
			reader.model.ContainerID = "replacement"
			reader.model.Deployment.ContainerID = "replacement"
			resolved, err := command.resolveCurrentIngestionModel(context.Background(), entity.InferenceIngestionJob{TenantID: capture.stored.TenantID, ModelID: capture.stored.ModelID, ContainerID: capture.stored.ContainerID, ModelVersion: capture.stored.ModelVersion})
			require.NoError(t, err)
			require.Equal(t, "replacement", resolved.ContainerID)
		})
	}
}

type legacyIngestionMetadata struct {
	dockerAPI.DockerInferenceAPIInterface
	model dockerAPI.ModelInfo
}

func (api *legacyIngestionMetadata) GetModelInfo(context.Context, string) (dockerAPI.GetModelInfoResponse, error) {
	return dockerAPI.GetModelInfoResponse{Data: api.model}, nil
}

func TestLegacyLogicalIDRequiresCurrentTenantContainerAndMatchingMetadata(t *testing.T) {
	reader := &ingestionRegistrationReader{model: entity.InferenceModel{ID: "firestore-document", TenantID: "tenant", ContainerID: "original"}}
	metadata := &legacyIngestionMetadata{model: dockerAPI.ModelInfo{ModelID: "CathEF_CLIP", Version: "1.0.1"}}
	command := &InferenceCommandService{InferenceQueryRepositoryInterface: reader, DockerSDKInterface: &guardedDispatchDockerSDK{}, DockerInferenceAPIInterface: metadata}
	job := entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "CathEF_CLIP", ContainerID: "original", ModelVersion: "1.0.1"}
	_, err := command.resolveCurrentIngestionModel(context.Background(), job)
	require.NoError(t, err)
	metadata.model.ModelID = "different-model"
	_, err = command.resolveCurrentIngestionModel(context.Background(), job)
	require.Error(t, err)
	metadata.model.ModelID = "CathEF_CLIP"
	metadata.model.Version = "2.0.0"
	_, err = command.resolveCurrentIngestionModel(context.Background(), job)
	require.ErrorContains(t, err, "model version changed")
}

func TestLogicalIDResolutionFailsClosedForAmbiguityAndInventoryErrors(t *testing.T) {
	model := entity.InferenceModel{ID: "document-a", TenantID: "tenant", ContainerID: "current-a", Deployment: &entity.InferenceModelDeployment{ContainerID: "current-a", ArtifactModelID: "CathEF_CLIP", ModelVersion: "1.0.1"}}
	second := model
	second.ID, second.ContainerID = "document-b", "current-b"
	secondDeployment := *model.Deployment
	secondDeployment.ContainerID = second.ContainerID
	second.Deployment = &secondDeployment
	reader := &ingestionRegistrationReader{models: []entity.InferenceModel{model, second}}
	sdk := &guardedDispatchDockerSDK{}
	command := &InferenceCommandService{InferenceQueryRepositoryInterface: reader, DockerSDKInterface: sdk}
	job := entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "CathEF_CLIP", ContainerID: "retained-old", ModelVersion: "1.0.1"}
	_, err := command.resolveCurrentIngestionModel(context.Background(), job)
	require.ErrorContains(t, err, "ambiguous")
	reader.err = context.Canceled
	_, err = command.resolveCurrentIngestionModel(context.Background(), job)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, sdk.inspectCalls, "inventory failure must not fall back to a retained Docker target")
}

func TestLogicalIDCannotBorrowCurrentContainerOfAnotherArtifact(t *testing.T) {
	reader := &ingestionRegistrationReader{model: entity.InferenceModel{ID: "document", TenantID: "tenant", ContainerID: "current", Deployment: &entity.InferenceModelDeployment{ContainerID: "current", ArtifactModelID: "different", ModelVersion: "1.0.1"}}}
	command := &InferenceCommandService{InferenceQueryRepositoryInterface: reader}
	_, err := command.resolveCurrentIngestionModel(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ModelID: "CathEF_CLIP", ContainerID: "current", ModelVersion: "1.0.1"})
	require.Error(t, err)
}

func TestStaleDeploymentSnapshotCannotAuthorizeHistoricalVersion(t *testing.T) {
	for _, staleField := range []string{"container", "image"} {
		t.Run(staleField, func(t *testing.T) {
			model := entity.InferenceModel{ID: "document", TenantID: "tenant", ContainerID: "current", DockerImage: "new-image", Deployment: &entity.InferenceModelDeployment{ContainerID: "current", ImageReference: "new-image", ArtifactModelID: "CathEF_CLIP", ModelVersion: "1.0.1"}}
			if staleField == "container" {
				model.Deployment.ContainerID = "retained-old"
			} else {
				model.Deployment.ImageReference = "old-image"
			}
			command := &InferenceCommandService{InferenceQueryRepositoryInterface: &ingestionRegistrationReader{model: model}, DockerSDKInterface: &guardedDispatchDockerSDK{}, DockerInferenceAPIInterface: &legacyIngestionMetadata{model: dockerAPI.ModelInfo{ModelID: "CathEF_CLIP", Version: "2.0.0"}}}
			for _, id := range []string{"CathEF_CLIP", "document"} {
				_, err := command.resolveCurrentIngestionModel(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ModelID: id, ContainerID: "retained-old", ModelVersion: "1.0.1"})
				require.Error(t, err, "stale moved snapshot cannot authorize a version")
				_, err = command.resolveCurrentIngestionModel(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ModelID: id, ContainerID: "current", ModelVersion: "1.0.1"})
				require.ErrorContains(t, err, "model version changed", "current metadata must replace stale snapshot")
			}
		})
	}
}

func TestNewJobsCannotSkipRegistrationAuthorizationWithEmptyModelID(t *testing.T) {
	capture := &registrationJobCapture{}
	command := &InferenceCommandService{InferenceCommandRepositoryInterface: capture}
	require.Error(t, command.CreateInferenceIngestionJob(context.Background(), serviceTypes.CreateInferenceIngestionJob{TenantID: "tenant", ContainerID: "unregistered"}))
	require.Empty(t, capture.stored.ID)
}
