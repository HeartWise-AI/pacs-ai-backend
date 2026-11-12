package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-pacs/infrastructures/providers/sdk/docker"
	"api-pacs/module/inference/domain/entity"
	engine "github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

func TestRequireMaintenanceRejectsEveryRunningAdmissionCaller(t *testing.T) {
	for _, name := range []string{"api-pacs", "study-service", "study-celery-worker", "study-celery-worker-deepcoro-cto", "study-celery-worker-deeprv-clip"} {
		t.Run(name, func(t *testing.T) {
			sdk, closeServer := maintenanceTestSDK(t, []engine.Container{
				{ID: "operator", Names: []string{"/gpu-placement-operator"}},
				{ID: "model", Names: []string{"/deepcoro-jepa"}},
				{ID: "caller", Names: []string{"/" + name}},
			}, http.StatusOK)
			defer closeServer()
			err := requireMaintenance(context.Background(), sdk)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("running caller was not rejected: %v", err)
			}
		})
	}
}

func TestRequireMaintenanceAllowsOperatorAndInferenceModels(t *testing.T) {
	sdk, closeServer := maintenanceTestSDK(t, []engine.Container{
		{ID: "operator", Names: []string{"/gpu-placement-operator"}},
		{ID: "model1", Names: []string{"/DeepCORO-CTO"}},
		{ID: "model2", Names: []string{"/deepcoro-jepa"}},
	}, http.StatusOK)
	defer closeServer()
	if err := requireMaintenance(context.Background(), sdk); err != nil {
		t.Fatal(err)
	}
}

func TestRequireMaintenanceInspectsEveryReturnedName(t *testing.T) {
	sdk, closeServer := maintenanceTestSDK(t, []engine.Container{
		{ID: "caller", Names: []string{"/unrelated-alias", "/study-service"}},
	}, http.StatusOK)
	defer closeServer()
	if err := requireMaintenance(context.Background(), sdk); err == nil {
		t.Fatal("secondary caller name bypassed guard")
	}
}

func TestRequireMaintenanceFailsClosedWhenDockerInventoryFails(t *testing.T) {
	sdk, closeServer := maintenanceTestSDK(t, nil, http.StatusInternalServerError)
	defer closeServer()
	if err := requireMaintenance(context.Background(), sdk); err == nil {
		t.Fatal("unavailable inventory allowed maintenance")
	}
}

func TestRequireMaintenanceHonorsCancellation(t *testing.T) {
	sdk, closeServer := maintenanceTestSDK(t, nil, http.StatusOK)
	defer closeServer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := requireMaintenance(ctx, sdk); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func maintenanceTestSDK(t *testing.T, containers []engine.Container, statusCode int) (*docker.DockerSDK, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1.47/containers/json" {
			t.Errorf("unexpected or mutating Docker request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		if value := r.URL.Query().Get("all"); value != "" && value != "0" {
			t.Errorf("guard should query running callers only: all=%s", value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if statusCode != http.StatusOK {
			json.NewEncoder(w).Encode(map[string]string{"message": "inventory unavailable"})
			return
		}
		json.NewEncoder(w).Encode(containers)
	}))
	c, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return &docker.DockerSDK{Client: c}, func() { c.Close(); server.Close() }
}

func TestReadOnlyDockerRefusesStartWithoutCallingEngine(t *testing.T) {
	sdk, closeServer := maintenanceTestSDK(t, nil, http.StatusOK)
	defer closeServer()
	if err := (readOnlyDocker{sdk}).StartContainer(context.Background(), "stopped-model"); err == nil {
		t.Fatal("read-only audit permitted a model start")
	}
	// The test server rejects every mutating request, including POST /start.
}

func TestTaggedImmutableReferenceSupportsValidatedSameArtifactCatalogEntry(t *testing.T) {
	const repositoryName = "docker.io/heartwisehub/deepcoro-jepa"
	digest := "sha256:" + strings.Repeat("a", 64)
	ref, err := taggedImmutableReference(repositoryName, "1.1.1", repositoryName+"@"+digest)
	if err != nil {
		t.Fatal(err)
	}
	if ref != repositoryName+":1.1.1@"+digest {
		t.Fatalf("tag or immutable digest lost: %s", ref)
	}
	revision := strings.Repeat("b", 40)
	version := entity.InferenceModelVersion{
		ModelID: "DeepCORO_JEPA", Version: "1.1.1", ImageReference: ref, ImageDigest: digest,
		Provenance: &entity.InferenceModelProvenance{
			SourceRepository: "HeartWise-AI/pacs-ai-backend", SourceRevision: &revision,
			ModelRepository: "heartwise/DeepCORO_JEPA", ModelRevision: revision,
			WeightsPath: "model.safetensors", WeightsSHA256: strings.Repeat("c", 64),
		},
	}
	version.Normalize()
	if err := version.Validate(); err != nil {
		t.Fatalf("same artifact missing-catalog entry invalid: %v", err)
	}
}

func TestTaggedImmutableReferenceRejectsMutableAndMismatchedArtifacts(t *testing.T) {
	const repositoryName = "docker.io/heartwisehub/deepcoro-jepa"
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, immutable := range []string{
		repositoryName + ":1.1.1", "docker.io/heartwisehub/other@" + digest,
	} {
		if _, err := taggedImmutableReference(repositoryName, "1.1.1", immutable); err == nil {
			t.Fatalf("unsafe artifact accepted: %s", immutable)
		}
	}
}
