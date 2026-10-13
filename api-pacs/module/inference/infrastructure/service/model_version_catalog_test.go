package service

import (
	"context"
	"strings"
	"testing"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/modelupgrade"
	"api-pacs/module/inference/infrastructure/service/types"
)

type modelVersionCatalog struct {
	published entity.InferenceModelVersion
	selected  entity.InferenceModelVersion
	versions  []entity.InferenceModelVersion
	err       error
}

func (catalog *modelVersionCatalog) PublishInferenceModelVersion(
	_ context.Context,
	version entity.InferenceModelVersion,
) (entity.InferenceModelVersion, error) {
	catalog.published = version
	return version, catalog.err
}

func (catalog *modelVersionCatalog) SelectInferenceModelVersion(
	context.Context,
	string,
) (entity.InferenceModelVersion, error) {
	return catalog.selected, catalog.err
}

func (catalog *modelVersionCatalog) ListInferenceModelVersions(
	context.Context,
	string,
) ([]entity.InferenceModelVersion, error) {
	return catalog.versions, catalog.err
}

type modelVersionUpgradeExecutor struct {
	request modelupgrade.UpgradeRequest
	result  modelupgrade.UpgradeResult
	err     error
}

func (executor *modelVersionUpgradeExecutor) Upgrade(
	_ context.Context,
	request modelupgrade.UpgradeRequest,
) (modelupgrade.UpgradeResult, error) {
	executor.request = request
	return executor.result, executor.err
}

type modelVersionQueryRepository struct {
	repository.InferenceQueryRepositoryInterface
	model entity.InferenceModel
	err   error
}

func (query *modelVersionQueryRepository) SelectInferenceModelByID(
	context.Context,
	string,
) (entity.InferenceModel, error) {
	return query.model, query.err
}

type modelVersionDocker struct {
	dockerTypes.DockerSDKInterface
	info  dockerTypes.GetContainerInfoResult
	calls int
}

func (docker *modelVersionDocker) GetContainerInfo(
	context.Context,
	string,
) (dockerTypes.GetContainerInfoResult, error) {
	docker.calls++
	return docker.info, nil
}

type modelVersionAPI struct {
	dockerInferenceTypes.DockerInferenceAPIInterface
	info  dockerInferenceTypes.GetModelInfoResponse
	calls int
}

func (api *modelVersionAPI) GetModelInfo(
	context.Context,
	string,
) (dockerInferenceTypes.GetModelInfoResponse, error) {
	api.calls++
	return api.info, nil
}

func validServiceModelVersion() entity.InferenceModelVersion {
	sourceRevision := strings.Repeat("a", 40)
	return entity.InferenceModelVersion{
		ID:             "catalog-version",
		ModelID:        "model-1",
		Version:        "2.0.0",
		ImageReference: "heartwisehub/model:2.0.0",
		ImageDigest:    "sha256:" + strings.Repeat("b", 64),
		Status:         entity.InferenceModelVersionAvailable,
		Provenance: &entity.InferenceModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend",
			SourceRevision:   &sourceRevision,
			ModelRepository:  "HeartWise-AI/model",
			ModelRevision:    strings.Repeat("c", 40),
			WeightsPath:      "weights/model.pt",
			WeightsSHA256:    strings.Repeat("d", 64),
		},
	}
}

func TestPublishInferenceModelVersionValidatesAndDelegates(t *testing.T) {
	catalog := &modelVersionCatalog{}
	service := InferenceCommandService{InferenceModelVersionRepositoryInterface: catalog}
	version := validServiceModelVersion()

	if _, err := service.PublishInferenceModelVersion(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	if catalog.published.ModelID != version.ModelID || catalog.published.Status != entity.InferenceModelVersionAvailable {
		t.Fatalf("published version = %+v", catalog.published)
	}

	version.ImageDigest = "invalid"
	if _, err := service.PublishInferenceModelVersion(context.Background(), version); err == nil || err.Error() != apiError.InvalidPayload {
		t.Fatalf("invalid catalog version returned %v", err)
	}
}

func TestDeployInferenceModelVersionPassesOnlyCatalogIdentity(t *testing.T) {
	executor := &modelVersionUpgradeExecutor{result: modelupgrade.UpgradeResult{
		Active: entity.InferenceModelDeployment{CatalogVersionID: "catalog-version"},
	}}
	service := InferenceCommandService{ModelUpgradeCoordinator: executor}

	result, err := service.DeployInferenceModelVersion(context.Background(), types.DeployInferenceModelVersion{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version",
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.request != (modelupgrade.UpgradeRequest{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-version",
	}) {
		t.Fatalf("upgrade request = %+v", executor.request)
	}
	if result.Active.CatalogVersionID != "catalog-version" {
		t.Fatalf("deployment result = %+v", result)
	}
}

func TestGetInferenceModelVersionsUsesDeploymentSnapshotAndMarksActive(t *testing.T) {
	active := validServiceModelVersion()
	older := validServiceModelVersion()
	older.ID, older.Version, older.ImageReference = "older", "1.0.0", "heartwisehub/model:1.0.0"
	catalog := &modelVersionCatalog{versions: []entity.InferenceModelVersion{active, older}}
	docker := &modelVersionDocker{}
	api := &modelVersionAPI{}
	service := InferenceQueryService{
		InferenceQueryRepositoryInterface: &modelVersionQueryRepository{model: entity.InferenceModel{
			TenantID: "tenant-1", ContainerID: "container", DockerImage: active.ImageReference,
			Deployment: &entity.InferenceModelDeployment{
				ArtifactModelID: active.ModelID, CatalogVersionID: active.ID,
			},
		}},
		InferenceModelVersionRepositoryInterface: catalog,
		DockerSDKInterface:                       docker,
		DockerInferenceAPIInterface:              api,
	}

	versions, err := service.GetInferenceModelVersions(context.Background(), types.GetInferenceModelVersions{
		TenantID: "tenant-1", ModelID: "registration-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || !versions[0].Active || versions[1].Active {
		t.Fatalf("catalog versions = %+v", versions)
	}
	if docker.calls != 0 || api.calls != 0 {
		t.Fatal("deployment snapshot unexpectedly triggered live container inspection")
	}
}

func TestGetInferenceModelVersionsFallsBackToLegacyModelInfo(t *testing.T) {
	version := validServiceModelVersion()
	catalog := &modelVersionCatalog{versions: []entity.InferenceModelVersion{version}}
	docker := &modelVersionDocker{info: dockerTypes.GetContainerInfoResult{Name: "/active-model"}}
	api := &modelVersionAPI{info: dockerInferenceTypes.GetModelInfoResponse{
		Data: dockerInferenceTypes.ModelInfo{ModelID: version.ModelID},
	}}
	service := InferenceQueryService{
		InferenceQueryRepositoryInterface: &modelVersionQueryRepository{model: entity.InferenceModel{
			TenantID: "tenant-1", ContainerID: "container", DockerImage: version.ImageReference,
		}},
		InferenceModelVersionRepositoryInterface: catalog,
		DockerSDKInterface:                       docker,
		DockerInferenceAPIInterface:              api,
	}

	versions, err := service.GetInferenceModelVersions(context.Background(), types.GetInferenceModelVersions{
		TenantID: "tenant-1", ModelID: "registration-1",
	})
	if err != nil || len(versions) != 1 || !versions[0].Active {
		t.Fatalf("legacy catalog lookup = %+v err=%v", versions, err)
	}
	if docker.calls != 1 || api.calls != 1 {
		t.Fatalf("legacy inspection calls docker=%d api=%d", docker.calls, api.calls)
	}
}

func TestGetInferenceModelVersionsDoesNotCrossTenantBoundary(t *testing.T) {
	service := InferenceQueryService{
		InferenceQueryRepositoryInterface: &modelVersionQueryRepository{
			model: entity.InferenceModel{TenantID: "tenant-2"},
		},
		InferenceModelVersionRepositoryInterface: &modelVersionCatalog{},
	}
	_, err := service.GetInferenceModelVersions(context.Background(), types.GetInferenceModelVersions{
		TenantID: "tenant-1", ModelID: "registration-1",
	})
	if err == nil || err.Error() != apiError.MissingRecord {
		t.Fatalf("cross-tenant lookup returned %v", err)
	}
}
