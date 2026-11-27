package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/domain/repository"
	inferenceService "api-pacs/module/inference/infrastructure/service"
)

const (
	lifecycleCallerTenant = "tenant-a"
	lifecycleOtherTenant  = "tenant-b"
	lifecycleCallerModel  = "model-doc-a"
	// Engine container IDs are the values CreateContainer stores. Names of
	// infrastructure containers are not among them.
	lifecycleCallerContainer = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lifecycleOtherContainer  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// lifecycleModels is the caller's tenant catalogue. A second read of the
// caller's container can report a different container ID, which is the
// record changing between a check and the Docker call.
type lifecycleModels struct {
	repository.InferenceQueryRepositoryInterface
	mu              sync.Mutex
	reads           int
	swapContainerTo string
}

func (models *lifecycleModels) caller() entity.InferenceModel {
	containerID := lifecycleCallerContainer
	if models.reads > 1 && models.swapContainerTo != "" {
		containerID = models.swapContainerTo
	}
	return entity.InferenceModel{
		ID:          lifecycleCallerModel,
		TenantID:    lifecycleCallerTenant,
		ContainerID: containerID,
	}
}

func (models *lifecycleModels) SelectInferenceModelByID(_ context.Context, id string) (entity.InferenceModel, error) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.reads++
	if id != lifecycleCallerModel {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	return models.caller(), nil
}

func (models *lifecycleModels) SelectInferenceModelByContainer(_ context.Context, tenantID, containerID string) (entity.InferenceModel, error) {
	models.mu.Lock()
	defer models.mu.Unlock()
	models.reads++
	if tenantID != lifecycleCallerTenant || containerID != lifecycleCallerContainer {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}
	return models.caller(), nil
}

func (models *lifecycleModels) SelectInferenceModels(_ context.Context, tenantID string) ([]entity.InferenceModel, error) {
	models.mu.Lock()
	defer models.mu.Unlock()
	if tenantID != lifecycleCallerTenant {
		return nil, errors.New(apiError.MissingRecord)
	}
	return []entity.InferenceModel{{
		ID:          lifecycleCallerModel,
		TenantID:    lifecycleCallerTenant,
		ContainerID: lifecycleCallerContainer,
	}}, nil
}

func (models *lifecycleModels) reset() {
	models.mu.Lock()
	models.reads = 0
	models.mu.Unlock()
}

type lifecycleEngine struct {
	mu    sync.Mutex
	calls []string
}

func (engine *lifecycleEngine) reset() {
	engine.mu.Lock()
	engine.calls = nil
	engine.mu.Unlock()
}

func (engine *lifecycleEngine) snapshot() []string {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	out := make([]string, len(engine.calls))
	copy(out, engine.calls)
	return out
}

func (engine *lifecycleEngine) handler(canonical string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		engine.mu.Lock()
		engine.calls = append(engine.calls, r.Method+" "+r.URL.Path)
		engine.mu.Unlock()
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"Id": canonical, "Name": "/tenant-model", "Image": "sha256:test",
				"Config": map[string]interface{}{"Labels": map[string]string{}},
				"State": map[string]interface{}{
					"Status": "running", "StartedAt": "2026-11-12T00:00:00Z", "FinishedAt": "0001-01-01T00:00:00Z",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func engineContainerRef(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// v1.47 / containers / <ref> / stop|start|restart|json
	if len(parts) >= 4 && parts[1] == "containers" {
		return parts[2]
	}
	return path
}

func lifecycleRouter(command *InferenceCommandController, query *InferenceQueryController) http.Handler {
	middleware := &iamMiddleware.IAMMiddleware{}
	router := chi.NewRouter()
	router.Route("/v1/inference/model", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(accessEntity.PermModelsManage))
			r.Route("/container", func(r chi.Router) {
				r.Post("/{containerID}/restart", command.RestartInferenceModelContainer)
				r.Post("/{containerID}/start", command.StartInferenceModelContainer)
				r.Post("/{containerID}/stop", command.StopInferenceModelContainer)
				r.Get("/{containerID}/info", query.GetContainerInfo)
			})
		})
	})
	return router
}

func lifecycleAccess(role string, manage bool) accessEntity.Access {
	if role == iamEntity.UserRole && manage {
		return accessEntity.ForRole(role, &accessEntity.CustomRole{
			ID: "role-managers", Name: "Managers", AllModels: true,
			Permissions: []string{accessEntity.PermModelsManage},
		})
	}
	return accessEntity.ForRole(role, nil)
}

func lifecycleRequest(method, containerRef, action string, access accessEntity.Access) *http.Request {
	request := httptest.NewRequest(method, "/v1/inference/model/container/"+url.PathEscape(containerRef)+"/"+action, nil)
	ctx := accessctx.WithAccess(request.Context(), access)
	ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, lifecycleCallerTenant)
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	ctx = context.WithValue(ctx, iamTypes.RoleCtx, access.Role)
	return request.WithContext(ctx)
}

func newLifecycleHarness(t *testing.T, models *lifecycleModels) (http.Handler, *lifecycleEngine) {
	t.Helper()
	engine := &lifecycleEngine{}
	server := httptest.NewServer(engine.handler(lifecycleCallerContainer))
	t.Cleanup(server.Close)
	dockerClient, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerClient.Close() })
	sdk := &dockerSDK.DockerSDK{Client: dockerClient}
	command := &InferenceCommandController{
		InferenceCommandServiceInterface: &inferenceService.InferenceCommandService{
			InferenceQueryRepositoryInterface: models,
			DockerSDKInterface:                sdk,
		},
	}
	query := &InferenceQueryController{
		InferenceQueryServiceInterface: &inferenceService.InferenceQueryService{
			InferenceQueryRepositoryInterface: models,
			DockerSDKInterface:                sdk,
		},
	}
	return lifecycleRouter(command, query), engine
}

func TestContainerLifecycleRejectsContainersTheCallerDoesNotOwn(t *testing.T) {
	models := &lifecycleModels{}
	handler, engine := newLifecycleHarness(t, models)
	holders := []struct {
		name   string
		access accessEntity.Access
	}{
		{name: "admin", access: lifecycleAccess(iamEntity.AdminRole, false)},
		{name: "user with models.manage", access: lifecycleAccess(iamEntity.UserRole, true)},
	}
	operations := []struct {
		method, action string
	}{
		{http.MethodPost, "stop"},
		{http.MethodPost, "start"},
		{http.MethodPost, "restart"},
		{http.MethodGet, "info"},
	}
	// Infrastructure names are fixed in the compose files. The other values are
	// another tenant's container, a short id the engine can prefix-match, and a
	// case variant of a host container name.
	targets := []string{
		"orthanc",
		"postgresql",
		"redis",
		"elasticsearch",
		"nginx",
		"api-pacs",
		lifecycleOtherContainer,
		lifecycleOtherContainer[:12],
		lifecycleCallerContainer[:12],
		"ORTHANC",
	}
	for _, holder := range holders {
		for _, operation := range operations {
			for _, target := range targets {
				t.Run(holder.name+" "+operation.action+" "+target, func(t *testing.T) {
					models.reset()
					engine.reset()
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, lifecycleRequest(operation.method, target, operation.action, holder.access))
					require.Empty(t, engine.snapshot(), "status %d", recorder.Code)
					require.GreaterOrEqual(t, recorder.Code, 400)
				})
			}
		}
	}
}

func TestContainerLifecycleActsOnlyOnTheCallerTenantContainer(t *testing.T) {
	models := &lifecycleModels{}
	handler, engine := newLifecycleHarness(t, models)
	access := lifecycleAccess(iamEntity.AdminRole, false)
	operations := []struct {
		method, action, suffix string
	}{
		{http.MethodPost, "stop", "/stop"},
		{http.MethodPost, "start", "/start"},
		{http.MethodPost, "restart", "/restart"},
		{http.MethodGet, "info", "/json"},
	}
	for _, operation := range operations {
		t.Run(operation.action, func(t *testing.T) {
			models.reset()
			engine.reset()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, lifecycleRequest(operation.method, lifecycleCallerContainer, operation.action, access))
			require.Equal(t, http.StatusOK, recorder.Code)
			calls := engine.snapshot()
			require.NotEmpty(t, calls)
			for _, call := range calls {
				path := strings.TrimPrefix(call, "POST ")
				path = strings.TrimPrefix(path, "GET ")
				require.Equal(t, lifecycleCallerContainer, engineContainerRef(path))
				require.True(t, strings.HasSuffix(path, operation.suffix))
			}
		})
	}
}

func TestContainerLifecycleDoesNotFollowAContainerIDThatChangesAfterAuthorization(t *testing.T) {
	models := &lifecycleModels{swapContainerTo: "orthanc"}
	handler, engine := newLifecycleHarness(t, models)
	access := lifecycleAccess(iamEntity.UserRole, true)
	operations := []struct {
		method, action string
	}{
		{http.MethodPost, "stop"},
		{http.MethodPost, "start"},
		{http.MethodPost, "restart"},
		{http.MethodGet, "info"},
	}
	for _, operation := range operations {
		t.Run(operation.action, func(t *testing.T) {
			models.reset()
			engine.reset()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, lifecycleRequest(operation.method, lifecycleCallerContainer, operation.action, access))
			for _, call := range engine.snapshot() {
				path := call
				if i := strings.IndexByte(call, ' '); i >= 0 {
					path = call[i+1:]
				}
				require.Equal(t, lifecycleCallerContainer, engineContainerRef(path), "engine call %s", call)
			}
			if len(engine.snapshot()) == 0 {
				require.GreaterOrEqual(t, recorder.Code, 400)
			} else {
				require.Equal(t, http.StatusOK, recorder.Code)
			}
		})
	}
}

func TestContainerLifecycleRequiresModelsManage(t *testing.T) {
	models := &lifecycleModels{}
	handler, engine := newLifecycleHarness(t, models)
	access := lifecycleAccess(iamEntity.UserRole, false)
	require.False(t, access.Has(accessEntity.PermModelsManage))
	for _, action := range []string{"stop", "start", "restart"} {
		t.Run(action, func(t *testing.T) {
			engine.reset()
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, lifecycleRequest(http.MethodPost, "orthanc", action, access))
			require.Equal(t, http.StatusForbidden, recorder.Code)
			require.Empty(t, engine.snapshot())
		})
	}
}
