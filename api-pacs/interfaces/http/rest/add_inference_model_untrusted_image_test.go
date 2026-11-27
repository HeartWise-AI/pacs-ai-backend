package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/distribution/reference"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	inferenceAPI "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerSDK "api-pacs/infrastructures/providers/sdk/docker"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	accessEntity "api-pacs/module/access/domain/entity"
	iamEntity "api-pacs/module/iam/domain/entity"
	inferenceRepository "api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/modelupgrade"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	inferenceService "api-pacs/module/inference/infrastructure/service"
	inferenceRest "api-pacs/module/inference/interfaces/http/rest"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

// tenantSuppliedEnv is the environment entry a tenant sends with model
// registration. Registration must not put it on a container.
const tenantSuppliedEnv = "MODEL_CALLBACK=http://attacker.example/collect"

// addModelDigest is a stand-in repository digest, not a credential.
const addModelDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestAddInferenceModelDoesNotRunUntrustedImages(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		custom    *accessEntity.CustomRole
		image     string
		forbidden bool
	}{
		{name: "admin foreign repository", role: iamEntity.AdminRole, image: "attacker/implant:1"},
		{name: "admin foreign registry", role: iamEntity.AdminRole, image: "evil.example/heartwisehub/model:2.0.0"},
		{name: "admin mutable tag", role: iamEntity.AdminRole, image: "heartwisehub/model:latest"},
		{name: "admin missing tag", role: iamEntity.AdminRole, image: "heartwisehub/model"},
		{name: "admin approved tag still drops tenant env", role: iamEntity.AdminRole, image: "heartwisehub/model:2.0.0"},
		{name: "admin normalized reference is what runs", role: iamEntity.AdminRole, image: "docker.io/heartwisehub/model:2.0.0"},
		{name: "admin digest is not replaced by its tag", role: iamEntity.AdminRole, image: "heartwisehub/model:2.0.0@" + addModelDigest},
		{
			name:   "user granted models.manage",
			role:   iamEntity.UserRole,
			custom: &accessEntity.CustomRole{ID: "role-1", Name: "model admin", Permissions: []string{accessEntity.PermModelsManage}, AllModels: true},
			image:  "attacker/implant:1",
		},
		{
			name:      "user without models.manage",
			role:      iamEntity.UserRole,
			image:     "attacker/implant:1",
			forbidden: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := newAddModelEngine(t)
			recorder := &recordingDocker{DockerSDK: engine.sdk}
			repository := &addModelRepository{}
			handler := newAddModelHandler(t, test.role, test.custom, recorder, repository)

			body := map[string]any{
				"name":        "tenant-model",
				"dockerImage": test.image,
				"envs":        []string{tenantSuppliedEnv},
				"outputMode":  "JSON",
				"tenantId":    "tenant-b",
			}
			payload, err := json.Marshal(body)
			require.NoError(t, err)
			request := authenticatedWorklistRequest(http.MethodPost, "/v1/inference/model/add")
			request.Body = io.NopCloser(bytes.NewReader(payload))
			request.ContentLength = int64(len(payload))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if test.forbidden {
				require.Equal(t, http.StatusForbidden, response.Code)
			}
			assertAddModelStaysInsideUpgradeImagePolicy(t, test.image, response.Code, recorder, repository, engine)
		})
	}
}

// assertAddModelStaysInsideUpgradeImagePolicy is the registration spec.
// An image the model-upgrade validator rejects must produce no pull, create,
// start, or persistence. An image it accepts may be pulled only as the
// normalized reference that passed validation, never as a tag substituted
// afterwards, and never with the tenant's environment. The started container
// must be the container created from that same reference.
func assertAddModelStaysInsideUpgradeImagePolicy(
	t *testing.T,
	submitted string,
	status int,
	docker *recordingDocker,
	repository *addModelRepository,
	engine *addModelEngine,
) {
	t.Helper()
	normalized, allowed := approvedAddModelReference(t, submitted)
	engine.mu.Lock()
	defer engine.mu.Unlock()

	if !allowed {
		if status == http.StatusCreated || len(docker.pulls) != 0 || len(docker.creates) != 0 || len(docker.starts) != 0 || len(repository.inserted) != 0 || len(engine.pulls) != 0 || len(engine.creates) != 0 || len(engine.starts) != 0 {
			t.Fatalf("untrusted image %q was registered (status %d): pulls=%q enginePulls=%v creates=%s starts=%v inserted=%d", submitted, status, docker.pulls, engine.pulls, formatAddModelCreates(engine.creates), docker.starts, len(repository.inserted))
		}
		return
	}
	if status == http.StatusCreated && len(docker.creates) == 0 {
		t.Fatalf("registration reported success without creating a container for %q", normalized)
	}
	for _, pull := range docker.pulls {
		if pull != normalized {
			t.Fatalf("PullImage(%q) after validating %q", pull, normalized)
		}
	}
	for _, pull := range engine.pulls {
		if pull != enginePullQuery(t, normalized) {
			t.Fatalf("daemon pull %q, validated reference %q encodes as %q", pull, normalized, enginePullQuery(t, normalized))
		}
	}
	created := map[string]bool{}
	for i, create := range docker.creates {
		if create.config.Image != normalized {
			t.Fatalf("CreateContainer image %q, validated reference %q", create.config.Image, normalized)
		}
		if slicesContain(create.config.Envs, tenantSuppliedEnv) {
			t.Fatalf("tenant environment was passed to CreateContainer: %v", create.config.Envs)
		}
		if create.id == "" {
			t.Fatalf("CreateContainer for %q did not return the container that would be started", normalized)
		}
		created[create.id] = true
		if i < len(engine.creates) {
			seen := engine.creates[i]
			if seen.image != normalized {
				t.Fatalf("daemon create image %q, validated reference %q", seen.image, normalized)
			}
			if slicesContain(seen.envs, tenantSuppliedEnv) {
				t.Fatalf("tenant environment reached the daemon: %v", seen.envs)
			}
			if seen.id != create.id {
				t.Fatalf("daemon container %q is not the container returned to registration %q", seen.id, create.id)
			}
		}
	}
	for _, id := range docker.starts {
		if !created[id] {
			t.Fatalf("StartContainer(%q) is not a container created from %q", id, normalized)
		}
	}
	for _, id := range engine.starts {
		if !created[id] {
			t.Fatalf("daemon start %q is not a container created from %q", id, normalized)
		}
	}
	for _, row := range repository.inserted {
		if row.TenantID != "tenant-a" {
			t.Fatalf("persisted tenant %q, session tenant is tenant-a", row.TenantID)
		}
		if row.DockerImage != normalized {
			t.Fatalf("persisted image %q, validated reference %q", row.DockerImage, normalized)
		}
		if row.ContainerID == "" || !created[row.ContainerID] {
			t.Fatalf("persisted container %q was not created from %q", row.ContainerID, normalized)
		}
		if slicesContain(row.Envs, tenantSuppliedEnv) {
			t.Fatalf("tenant environment was persisted: %v", row.Envs)
		}
	}
}

func approvedAddModelReference(t *testing.T, image string) (string, bool) {
	t.Helper()
	validator, err := modelupgrade.NewCandidateValidator(addModelValidatorDocker{}, addModelValidatorAPI{}, modelupgrade.CandidateValidationConfig{
		AllowedImageNamespace: "heartwisehub",
		ReadinessTimeout:      time.Second,
		PollInterval:          time.Millisecond,
	})
	require.NoError(t, err)
	normalized, err := validator.ValidateImageReference(image, "")
	if err != nil {
		return "", false
	}
	return normalized, true
}

func enginePullQuery(t *testing.T, image string) string {
	t.Helper()
	parsed, err := reference.ParseNormalizedNamed(image)
	require.NoError(t, err)
	fromImage := reference.FamiliarName(parsed)
	tag := ""
	if digested, ok := parsed.(reference.Digested); ok {
		tag = digested.Digest().String()
	} else {
		taggedRef := reference.TagNameOnly(parsed)
		if tagged, ok := taggedRef.(reference.Tagged); ok {
			tag = tagged.Tag()
		}
	}
	return fromImage + ":" + tag
}

func formatAddModelCreates(creates []engineCreate) string {
	parts := make([]string, 0, len(creates))
	for _, create := range creates {
		parts = append(parts, create.image+" networks="+strings.Join(create.networks, ",")+" dns="+strings.Join(create.dns, ",")+" id="+create.id)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func newAddModelHandler(t *testing.T, role string, custom *accessEntity.CustomRole, docker dockerTypes.DockerSDKInterface, repository inferenceRepository.InferenceCommandRepositoryInterface) http.Handler {
	t.Helper()
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: role},
		UserQueryServiceInterface: &worklistPolicyQueryService{
			status: userServiceTypes.PolicyStatus{EnforcementActive: true},
		},
		AccessResolver: &stubAccessResolver{custom: custom},
	}
	controller := &inferenceRest.InferenceCommandController{}
	controller.InferenceCommandServiceInterface = &inferenceService.InferenceCommandService{
		DockerSDKInterface:                  docker,
		InferenceCommandRepositoryInterface: repository,
	}

	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) {
		r.Use(requestbody.Limit(requestbody.DefaultAPIMaxBytes))
		r.Route("/inference", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(middleware.TokenSessionAuthGuard)
				r.Use(middleware.PolicyAcceptanceGuard)
				r.Route("/model", func(r chi.Router) {
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequirePermission(accessEntity.PermModelsManage))
						r.Post("/add", controller.AddInferenceModel)
					})
				})
			})
		})
	})
	return router
}

type recordingDocker struct {
	*dockerSDK.DockerSDK
	pulls   []string
	creates []recordedCreate
	starts  []string
}

type recordedCreate struct {
	config dockerTypes.CreateContainer
	id     string
}

func (recorder *recordingDocker) PullImage(ctx context.Context, imageName string) error {
	recorder.pulls = append(recorder.pulls, imageName)
	return recorder.DockerSDK.PullImage(ctx, imageName)
}

func (recorder *recordingDocker) CreateContainer(ctx context.Context, config dockerTypes.CreateContainer) (string, error) {
	id, err := recorder.DockerSDK.CreateContainer(ctx, config)
	recorder.creates = append(recorder.creates, recordedCreate{config: config, id: id})
	return id, err
}

func (recorder *recordingDocker) StartContainer(ctx context.Context, containerID string) error {
	recorder.starts = append(recorder.starts, containerID)
	return recorder.DockerSDK.StartContainer(ctx, containerID)
}

type addModelRepository struct {
	inferenceRepository.InferenceCommandRepositoryInterface
	inserted []repositoryTypes.AddInferenceModel
}

func (repository *addModelRepository) InsertInferenceModel(_ context.Context, data repositoryTypes.AddInferenceModel) error {
	repository.inserted = append(repository.inserted, data)
	return nil
}

type engineCreate struct {
	image    string
	envs     []string
	networks []string
	dns      []string
	id       string
}

type addModelEngine struct {
	sdk     *dockerSDK.DockerSDK
	mu      sync.Mutex
	pulls   []string
	creates []engineCreate
	starts  []string
}

func newAddModelEngine(t *testing.T) *addModelEngine {
	t.Helper()
	engine := &addModelEngine{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/info"):
			_, _ = io.WriteString(w, `{"ID":"engine","Runtimes":{}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
			engine.mu.Lock()
			engine.pulls = append(engine.pulls, r.URL.Query().Get("fromImage")+":"+r.URL.Query().Get("tag"))
			engine.mu.Unlock()
			_, _ = io.WriteString(w, "{\"status\":\"Pull complete\"}\n")
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var request struct {
				container.Config
				HostConfig       *container.HostConfig     `json:"HostConfig"`
				NetworkingConfig *network.NetworkingConfig `json:"NetworkingConfig"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			id := "container-created"
			networks := make([]string, 0)
			if request.NetworkingConfig != nil {
				for name := range request.NetworkingConfig.EndpointsConfig {
					networks = append(networks, name)
				}
			}
			dns := []string{}
			if request.HostConfig != nil {
				dns = append(dns, request.HostConfig.DNS...)
			}
			engine.mu.Lock()
			engine.creates = append(engine.creates, engineCreate{
				image: request.Image, envs: append([]string(nil), request.Env...), networks: networks, dns: dns, id: id,
			})
			engine.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Warnings": []string{}})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/start"):
			id := strings.TrimSuffix(r.URL.Path, "/start")
			id = id[strings.LastIndex(id, "/")+1:]
			engine.mu.Lock()
			engine.starts = append(engine.starts, id)
			engine.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	sdkClient, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sdkClient.Close() })
	engine.sdk = &dockerSDK.DockerSDK{Client: sdkClient, Network: "pacs-net"}
	return engine
}

type addModelValidatorDocker struct{}

func (addModelValidatorDocker) InspectImage(context.Context, string) (dockerTypes.InspectImageResult, error) {
	return dockerTypes.InspectImageResult{}, nil
}

func (addModelValidatorDocker) GetContainerInfo(context.Context, string) (dockerTypes.GetContainerInfoResult, error) {
	return dockerTypes.GetContainerInfoResult{}, nil
}

func (addModelValidatorDocker) StartContainer(context.Context, string) error { return nil }

type addModelValidatorAPI struct{}

func (addModelValidatorAPI) GetModelInfo(context.Context, string) (inferenceAPI.GetModelInfoResponse, error) {
	return inferenceAPI.GetModelInfoResponse{}, nil
}

func (addModelValidatorAPI) GetModelRuntime(context.Context, string) (inferenceAPI.ModelRuntime, error) {
	return inferenceAPI.ModelRuntime{}, nil
}
