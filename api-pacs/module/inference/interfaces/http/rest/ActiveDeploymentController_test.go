package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	dockerAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repository "api-pacs/module/inference/domain/repository"
	service "api-pacs/module/inference/infrastructure/service"
	"github.com/stretchr/testify/require"
)

// A synthetic Docker/registration provider. Tests exercise the real registry,
// authorization, identity pinning and prediction handlers without patient data,
// GPUs, credentials, or mutations to any installed inference container.
type deploymentFixture struct {
	repository.InferenceQueryRepositoryInterface
	dockerTypes.DockerSDKInterface
	mu      sync.Mutex
	version string
	removed bool
	calls   int
}

func (f *deploymentFixture) model() entity.InferenceModel {
	id := strings.Repeat("a", 64)
	if f.version == "9.1.0" {
		id = strings.Repeat("b", 64)
	}
	return entity.InferenceModel{ID: "fixture-registration", TenantID: "fixture-tenant", Name: "DeepCORO-CTO", ContainerID: id,
		Deployment: &entity.InferenceModelDeployment{ContainerID: id, ModelVersion: f.version, LocalImageID: "sha256:" + id,
			CatalogVersionID: "fixture-" + f.version, ImageDigest: "sha256:" + id}}
}
func (f *deploymentFixture) ListActiveModelRegistrations(context.Context) ([]entity.InferenceModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.removed {
		return []entity.InferenceModel{}, nil
	}
	return []entity.InferenceModel{f.model()}, nil
}
func (f *deploymentFixture) SelectInferenceModelByID(_ context.Context, id string) (entity.InferenceModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	model := f.model()
	if f.removed || id != model.ID {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	return model, nil
}
func (f *deploymentFixture) SelectInferenceModelByContainer(ctx context.Context, tenant, container string) (entity.InferenceModel, error) {
	model, err := f.SelectInferenceModelByID(ctx, "fixture-registration")
	if err != nil || model.TenantID != tenant || model.ContainerID != container {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	return model, nil
}
func (f *deploymentFixture) GetContainerInfo(_ context.Context, id string) (dockerTypes.GetContainerInfoResult, error) {
	// Both versions remain inspectable to simulate retained rollback containers.
	return dockerTypes.GetContainerInfoResult{ID: id, Name: "/fixture-model", ImageID: "sha256:" + id}, nil
}
func (f *deploymentFixture) Predict(_ context.Context, id string, request dockerAPI.PredictRequest) (dockerAPI.PredictResponse, error) {
	if request.Stream != nil {
		if _, err := io.Copy(io.Discard, request.Stream); err != nil {
			return dockerAPI.PredictResponse{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	version := "9.0.0"
	if id == strings.Repeat("b", 64) {
		version = "9.1.0"
	}
	return dockerAPI.PredictResponse{Success: true, Data: map[string]interface{}{"version": version, "containerId": id}}, nil
}

func newDeploymentFixture() (*deploymentFixture, *http.ServeMux) {
	f := &deploymentFixture{version: "9.0.0"}
	svc := &service.InferenceCommandService{InferenceQueryRepositoryInterface: f, DockerSDKInterface: f, ModelManager: f}
	controller := &InferenceCommandController{InferenceCommandServiceInterface: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/v1/inference/deployments", controller.GetActiveDeployments)
	mux.HandleFunc("/internal/v1/inference/predict", controller.PredictPreparedInferenceModel)
	mux.HandleFunc("/fixture/version", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		version := r.URL.Query().Get("version")
		if version != "9.0.0" && version != "9.1.0" && version != "removed" {
			w.WriteHeader(400)
			return
		}
		f.removed = version == "removed"
		if !f.removed {
			f.version = version
		}
		w.WriteHeader(204)
	})
	return f, mux
}

func TestActiveDeploymentHTTPAuthenticationAndPinning(t *testing.T) {
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "fixture-token")
	f, mux := newDeploymentFixture()
	get := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/internal/v1/inference/deployments", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, 401, get("bad").Code)
	response := get("fixture-token")
	require.Equal(t, 200, response.Code)
	var snapshot entity.ActiveDeploymentRegistry
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
	identity, err := json.Marshal(snapshot.Models[0])
	require.NoError(t, err)
	predict := func(tenant string, large bool) *httptest.ResponseRecorder {
		payload := `{"tenantId":"` + tenant + `","containerRef":"mutable-alias","request":{"outputMode":"JSON"}}`
		if large {
			payload = `{"tenantId":"` + tenant + `","containerRef":"mutable-alias","request":{"padding":"` + strings.Repeat("x", managedFastPathMinBytes) + `"}}`
		}
		req := httptest.NewRequest("POST", "/internal/v1/inference/predict", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer fixture-token")
		req.Header.Set("X-PACS-Deployment", string(identity))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, 200, predict("fixture-tenant", false).Code)
	require.Equal(t, 200, predict("fixture-tenant", true).Code)
	require.Equal(t, 400, predict("other-tenant", false).Code)
	f.version = "9.1.0"
	require.Equal(t, 409, predict("fixture-tenant", false).Code)
	require.Equal(t, 409, predict("fixture-tenant", true).Code)
	require.Equal(t, 2, f.calls)
}

// Opt-in cross-repository HTTP fixture. Bind only to loopback when running on
// the host, or publish the container port to host loopback. Always self-expires.
func TestDeploymentRegistryFixtureServer(t *testing.T) {
	address := os.Getenv("DEPLOYMENT_REGISTRY_FIXTURE_ADDR")
	if address == "" {
		t.Skip("set DEPLOYMENT_REGISTRY_FIXTURE_ADDR for paired Python integration")
	}
	t.Setenv("STUDY_SERVICE_CALLBACK_TOKEN", "fixture-token")
	_, mux := newDeploymentFixture()
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	mux.HandleFunc("/fixture/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
		select {
		case <-done:
		default:
			close(done)
		}
	})
	go server.Serve(listener)
	fmt.Printf("DEPLOYMENT_REGISTRY_FIXTURE_READY=%s\n", listener.Addr())
	select {
	case <-done:
	case <-time.After(10 * time.Minute):
	}
	server.Close()
}
