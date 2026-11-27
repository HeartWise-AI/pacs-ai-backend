package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	"github.com/stretchr/testify/require"
)

type activeRegistryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	models []entity.InferenceModel
}

type registryImageSDK struct {
	managedGatewayDockerSDK
	image dockerTypes.InspectImageResult
}

func (d *registryImageSDK) InspectImage(context.Context, string) (dockerTypes.InspectImageResult, error) {
	return d.image, nil
}

func TestActiveRegistryLegacyLabelsAndMetadata(t *testing.T) {
	container := strings.Repeat("c", 64)
	repo := &activeRegistryRepository{models: []entity.InferenceModel{{ID: "legacy", TenantID: "tenant", Name: "Dummy", ContainerID: container}}}
	sdk := &registryImageSDK{managedGatewayDockerSDK: managedGatewayDockerSDK{info: dockerTypes.GetContainerInfoResult{ID: container, ImageID: "sha256:dummy", Name: "/dummy"}},
		image: dockerTypes.InspectImageResult{ID: "sha256:dummy", Labels: map[string]string{"org.opencontainers.image.title": "Dummy", "org.opencontainers.image.version": "9.0.0"}}}
	api := &viewerModelInfoAPI{info: dockerInferenceTypes.ModelInfo{ModelName: "Dummy", Version: "9.1.0"}}
	svc := &InferenceCommandService{InferenceQueryRepositoryInterface: repo, DockerSDKInterface: sdk, DockerInferenceAPIInterface: api}
	registry, err := svc.ActiveDeployments(context.Background())
	require.NoError(t, err)
	require.Equal(t, "9.0.0", registry.Models[0].ModelVersion)
	// Inherited OS labels are not accepted as the model version.
	sdk.image.Labels["org.opencontainers.image.title"] = "Ubuntu"
	registry, err = svc.ActiveDeployments(context.Background())
	require.NoError(t, err)
	require.Equal(t, "9.1.0", registry.Models[0].ModelVersion)
	// A display-name alias must not change the canonical adapter name.
	repo.models[0].Name = "My custom display name"
	registry, err = svc.ActiveDeployments(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Dummy", registry.Models[0].ModelName)
	// Existing deployment snapshots also recover the name from runtime metadata.
	repo.models[0].Deployment = &entity.InferenceModelDeployment{ContainerID: container, LocalImageID: "sha256:dummy", ModelVersion: "9.1.0"}
	registry, err = svc.ActiveDeployments(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Dummy", registry.Models[0].ModelName)
	selected, err := svc.ResolveDispatchDeployment(context.Background(), entity.InferenceIngestionJob{TenantID: "tenant", ContainerID: container, ModelName: "Dummy", ModelVersion: "9.1.0"})
	require.NoError(t, err)
	require.Equal(t, "Dummy", selected.ModelName)
	// Verified release labels also support a cold custom-named registration.
	sdk.image.Labels["org.opencontainers.image.title"] = "Dummy"
	sdk.image.Labels["org.opencontainers.image.version"] = "9.1.0"
	sdk.image.Labels["ai.heartwise.model.repository"] = "heartwise/dummy"
	api.err = errors.New("cold")
	registry, err = svc.ActiveDeployments(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Dummy", registry.Models[0].ModelName)
	api.err = nil
	sdk.image.Labels = nil
	api.info.ModelName = ""
	registry, err = svc.ActiveDeployments(context.Background())
	require.NoError(t, err)
	require.Equal(t, "UNAVAILABLE", registry.Models[0].State)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.ActiveDeployments(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestActiveRegistryNotificationRetriesAndStops(t *testing.T) {
	svc := &InferenceCommandService{InferenceQueryRepositoryInterface: &activeRegistryRepository{}}
	requests := make(chan string, 10)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path + " " + r.Header.Get("Authorization")
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		svc.runDeploymentNotifications(ctx, server.URL, "dummy-token", time.Millisecond)
		close(done)
	}()
	for i := 0; i < 2; i++ {
		select {
		case request := <-requests:
			require.Equal(t, "POST /ingest/deployments/refresh Bearer dummy-token", request)
		case <-time.After(2 * time.Second):
			t.Fatal("startup notification was not retried")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification loop did not stop")
	}
}

func (r *activeRegistryRepository) ListActiveModelRegistrations(context.Context) ([]entity.InferenceModel, error) {
	return r.models, nil
}
func (r *activeRegistryRepository) SelectInferenceModelByID(_ context.Context, id string) (entity.InferenceModel, error) {
	for _, m := range r.models {
		if m.ID == id {
			return m, nil
		}
	}
	return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
}
func (r *activeRegistryRepository) SelectInferenceModelByContainer(_ context.Context, tenant, id string) (entity.InferenceModel, error) {
	for _, m := range r.models {
		if m.TenantID == tenant && m.ContainerID == id {
			return m, nil
		}
	}
	return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
}

func TestActiveRegistryUpgradeRollbackRemovalAndTenantIsolation(t *testing.T) {
	container := strings.Repeat("a", 64)
	model := entity.InferenceModel{ID: "registration", TenantID: "tenant-a", Name: "Dummy", ContainerID: container,
		Deployment: &entity.InferenceModelDeployment{ContainerID: container, ModelName: "Dummy", ModelVersion: "9.0.0", LocalImageID: "sha256:image-a", CatalogVersionID: "catalog-a", ImageDigest: "sha256:digest-a"}}
	repo := &activeRegistryRepository{models: []entity.InferenceModel{model}}
	docker := &managedGatewayDockerSDK{info: dockerTypes.GetContainerInfoResult{ID: container, ImageID: "sha256:image-a", Name: "/dummy"}}
	predictor := &recordingManagedPredictor{response: dockerInferenceTypes.PredictResponse{Success: true}}
	svc := &InferenceCommandService{InferenceQueryRepositoryInterface: repo, DockerSDKInterface: docker, ModelManager: predictor}
	ctx := context.Background()
	first, err := svc.ActiveDeployments(ctx)
	require.NoError(t, err)
	require.Len(t, first.Models, 1)
	require.Equal(t, "9.0.0", first.Models[0].ModelVersion)
	expected := first.Models[0]
	orthancID := "synthetic-study"
	dispatcher := &StudyServiceDispatcher{DeploymentResolver: svc}
	input := serviceTypes.BuildStudyServiceDispatchRequestInput{
		OrthancStudyID: &orthancID,
		IngestionJob:   entity.InferenceIngestionJob{TenantID: "tenant-a", ContainerID: container, ModelName: "Dummy", ModelVersion: "9.0.0", Modalities: []string{"XA"}},
		Candidate:      entity.InferenceIngestionCandidate{TenantID: "tenant-a", StudyInstanceUID: "1.2.3"},
	}
	request, err := dispatcher.BuildDispatchStudyRequest(ctx, input)
	require.NoError(t, err)
	require.Equal(t, &expected, request.Deployment)
	input.IngestionJob.ModelVersion = "8.0.0"
	_, err = dispatcher.BuildDispatchStudyRequest(ctx, input)
	require.EqualError(t, err, apiError.InferenceUpgradeConflict)
	pinnedCtx := entity.WithExpectedDeployment(ctx, expected)
	_, err = svc.PredictPreparedInferenceModel(pinnedCtx, "tenant-a", "mutable-alias", dockerInferenceTypes.PredictRequest{})
	require.NoError(t, err)
	require.Equal(t, container, predictor.containerID)
	require.Equal(t, 1, predictor.calls)
	_, err = svc.PredictPreparedInferenceModel(pinnedCtx, "tenant-b", "dummy", dockerInferenceTypes.PredictRequest{})
	require.Error(t, err)
	require.Equal(t, 1, predictor.calls)

	// Upgrade while the old container remains available for rollback.
	updated := *model.Deployment
	updated.ModelVersion, updated.CatalogVersionID = "9.1.0", "catalog-b"
	repo.models[0].Deployment = &updated
	second, err := svc.ActiveDeployments(ctx)
	require.NoError(t, err)
	require.NotEqual(t, first.Revision, second.Revision)
	_, err = svc.PredictPreparedInferenceModel(pinnedCtx, "tenant-a", "dummy", dockerInferenceTypes.PredictRequest{})
	require.EqualError(t, err, apiError.InferenceUpgradeConflict)
	require.Equal(t, 1, predictor.calls)

	repo.models[0].Deployment = model.Deployment
	rolledBack, err := svc.ActiveDeployments(ctx)
	require.NoError(t, err)
	require.Equal(t, first.Revision, rolledBack.Revision)
	_, err = svc.PinPreparedDeployment(ctx, "tenant-a", expected)
	require.NoError(t, err)
	docker.info.ImageID = "sha256:unexpected"
	unavailable, err := svc.ActiveDeployments(ctx)
	require.NoError(t, err)
	require.Equal(t, "UNAVAILABLE", unavailable.Models[0].State)
	_, err = svc.PinPreparedDeployment(ctx, "tenant-a", expected)
	require.Error(t, err)
	repo.models = nil
	empty, err := svc.ActiveDeployments(ctx)
	require.NoError(t, err)
	require.Empty(t, empty.Models)
	_, err = svc.PinPreparedDeployment(ctx, "tenant-a", expected)
	require.Error(t, err)
}
