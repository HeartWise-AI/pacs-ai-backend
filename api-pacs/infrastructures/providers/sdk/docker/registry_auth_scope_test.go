package docker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/distribution/reference"
	distauth "github.com/docker/distribution/registry/client/auth"
	disttransport "github.com/docker/distribution/registry/client/transport"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/dockerversion"
	dockreg "github.com/docker/docker/registry"
	"github.com/opencontainers/go-digest"

	sdk "api-pacs/infrastructures/providers/sdk/docker"
	dockertypes "api-pacs/infrastructures/providers/sdk/docker/types"
	iamtypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/domain/entity"
	domainrepo "api-pacs/module/inference/domain/repository"
	repotypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service"
	inferencehttp "api-pacs/module/inference/interfaces/http/rest"
)

// Placeholders only. These are not credentials from any deployment.
const (
	operatorUsername = "operator-username"
	operatorPassword = "operator-password"
)

// registryDouble answers the /v2/ challenge PingV2Registry records, then the
// manifest or token request the engine authorizes afterwards.
type registryDouble struct {
	server *httptest.Server
	mode   string

	mu       sync.Mutex
	operator int
}

func newRegistryDouble(t *testing.T, mode string) *registryDouble {
	t.Helper()
	reg := &registryDouble{mode: mode}
	reg.server = httptest.NewServer(http.HandlerFunc(reg.serve))
	t.Cleanup(reg.server.Close)
	return reg
}

func (reg *registryDouble) serve(w http.ResponseWriter, r *http.Request) {
	if reg.acceptsOperator(r) {
		reg.mu.Lock()
		reg.operator++
		reg.mu.Unlock()
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"issued","access_token":"issued"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && reg.sawOperator() {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.Path == "/token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if strings.Contains(r.URL.Path, "/v2/") {
		w.Header().Set("WWW-Authenticate", reg.challenge(r))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.NotFound(w, r)
}

func (reg *registryDouble) challenge(r *http.Request) string {
	if reg.mode == "bearer" {
		realm := "http://" + r.Host + "/token"
		return fmt.Sprintf(`Bearer realm=%q,service="registry.attacker.example"`, realm)
	}
	return `Basic realm="registry.attacker.example"`
}

func (reg *registryDouble) acceptsOperator(r *http.Request) bool {
	user, password, ok := r.BasicAuth()
	if ok && user == operatorUsername && password == operatorPassword {
		return true
	}
	_ = r.ParseForm()
	return r.PostFormValue("username") == operatorUsername && r.PostFormValue("password") == operatorPassword
}

func (reg *registryDouble) sawOperator() bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return reg.operator > 0
}

// daemonDouble is the Engine API the product client calls. Pulls use the
// decode, endpoint lookup, and authorizer from Docker 27.3.1 with the
// containerd snapshotter left off.
type daemonDouble struct {
	server *httptest.Server
	hub    *registryDouble
	t      *testing.T
}

func newDaemonDouble(t *testing.T, hub *registryDouble) *daemonDouble {
	t.Helper()
	daemon := &daemonDouble{hub: hub, t: t}
	daemon.server = httptest.NewServer(http.HandlerFunc(daemon.serve))
	t.Cleanup(daemon.server.Close)
	return daemon
}

func (daemon *daemonDouble) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/_ping":
		w.Header().Set("API-Version", "1.47")
		w.Header().Set("OSType", "linux")
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
		daemon.pull(w, r)
	case strings.HasSuffix(r.URL.Path, "/info"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ID":"daemon","Runtimes":{}}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Id":"candidate"}`))
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/start"):
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (daemon *daemonDouble) pull(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := r.ParseForm(); err != nil {
		writePullError(w)
		return
	}
	ref, err := imageRef(r.Form.Get("fromImage"), r.Form.Get("tag"))
	if err != nil {
		writePullError(w)
		return
	}
	// postImagesCreate ignores a malformed auth header and still pulls.
	authConfig, _ := registry.DecodeAuthConfig(r.Header.Get(registry.AuthHeader))
	if err := daemon.pullRef(r.Context(), ref, authConfig); err != nil {
		daemon.t.Logf("pull %s: %v", ref.String(), err)
		writePullError(w)
		return
	}
	_, _ = w.Write([]byte("{\"status\":\"Downloaded newer image\"}\n"))
}

func imageRef(fromImage, tag string) (reference.Named, error) {
	ref, err := reference.ParseNormalizedNamed(strings.TrimSuffix(fromImage, ":"))
	if err != nil {
		return nil, err
	}
	if tag == "" {
		return ref, nil
	}
	if parsed, digestErr := digest.Parse(tag); digestErr == nil {
		return reference.WithDigest(reference.TrimNamed(ref), parsed)
	}
	return reference.WithTag(ref, tag)
}

func writePullError(w http.ResponseWriter) {
	_, _ = w.Write([]byte("{\"errorDetail\":{\"message\":\"pull failed\"},\"error\":\"pull failed\"}\n"))
}

func (daemon *daemonDouble) pullRef(ctx context.Context, ref reference.Named, authConfig *registry.AuthConfig) error {
	svc, err := dockreg.NewService(dockreg.ServiceOptions{})
	if err != nil {
		return err
	}
	repoInfo, err := svc.ResolveRepository(ref)
	if err != nil {
		return err
	}
	endpoints, err := svc.LookupPullEndpoints(reference.Domain(repoInfo.Name))
	if err != nil {
		return err
	}
	var lastErr error
	for _, endpoint := range endpoints {
		endpoint, err = daemon.standInOfficialRegistry(endpoint)
		if err != nil {
			lastErr = err
			continue
		}
		if err = authorizePull(ctx, ref, repoInfo, endpoint, authConfig); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no registry endpoint")
	}
	return lastErr
}

// standInOfficialRegistry keeps LookupPullEndpoints' choice of registry-1.docker.io
// and serves it locally. Other endpoints keep the URL the engine would dial.
func (daemon *daemonDouble) standInOfficialRegistry(endpoint dockreg.APIEndpoint) (dockreg.APIEndpoint, error) {
	if endpoint.URL == nil || endpoint.URL.Host != dockreg.DefaultRegistryHost {
		return endpoint, nil
	}
	if daemon.hub == nil {
		return endpoint, fmt.Errorf("docker hub endpoint was not stood in")
	}
	stood, err := url.Parse(daemon.hub.server.URL)
	if err != nil {
		return endpoint, err
	}
	endpoint.URL = stood
	return endpoint, nil
}

func authorizePull(ctx context.Context, ref reference.Named, repoInfo *dockreg.RepositoryInfo, endpoint dockreg.APIEndpoint, authConfig *registry.AuthConfig) error {
	if authConfig == nil {
		authConfig = &registry.AuthConfig{}
	}
	repoName := repoInfo.Name.Name()
	if endpoint.TrimHostname {
		repoName = reference.Path(repoInfo.Name)
	}
	direct := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	base := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         direct.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     endpoint.TLSConfig,
		DisableKeepAlives:   true,
	}
	modifiers := dockreg.Headers(dockerversion.DockerUserAgent(ctx), nil)
	authTransport := disttransport.NewTransport(base, modifiers...)
	challengeManager, err := dockreg.PingV2Registry(endpoint.URL, authTransport)
	if err != nil {
		return err
	}
	if authConfig.RegistryToken != "" {
		return fmt.Errorf("registry token auth is not the configured credential")
	}
	creds := dockreg.NewStaticCredentialStore(authConfig)
	tokenHandler := distauth.NewTokenHandlerWithOptions(distauth.TokenHandlerOptions{
		Transport:   authTransport,
		Credentials: creds,
		Scopes: []distauth.Scope{distauth.RepositoryScope{
			Repository: repoName,
			Actions:    []string{"pull"},
			Class:      repoInfo.Class,
		}},
		ClientID: dockreg.AuthClientID,
	})
	basicHandler := distauth.NewBasicHandler(creds)
	modifiers = append(modifiers, distauth.NewAuthorizer(challengeManager, tokenHandler, basicHandler))
	roundTrip := disttransport.NewTransport(base, modifiers...)
	tag := manifestTag(ref)
	manifest := strings.TrimRight(endpoint.URL.String(), "/") + "/v2/" + repoName + "/manifests/" + tag
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest, nil)
	if err != nil {
		return err
	}
	response, err := roundTrip.RoundTrip(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("manifest status %d", response.StatusCode)
	}
	return nil
}

func manifestTag(name reference.Named) string {
	if tagged, ok := name.(reference.Tagged); ok {
		return tagged.Tag()
	}
	if digested, ok := name.(reference.Digested); ok {
		return digested.Digest().String()
	}
	return "latest"
}

func newOperatorSDK(t *testing.T, daemon *daemonDouble) *sdk.DockerSDK {
	t.Helper()
	t.Setenv("DOCKER_HOST", daemon.server.URL)
	client, err := sdk.NewClient(dockertypes.Config{
		Username: operatorUsername,
		Password: operatorPassword,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Client.Close() })
	return client
}

func TestPullImageDoesNotPresentOperatorCredentialsToForeignRegistry(t *testing.T) {
	for _, mode := range []string{"basic", "bearer"} {
		t.Run(mode, func(t *testing.T) {
			foreign := newRegistryDouble(t, mode)
			hub := newRegistryDouble(t, "basic")
			daemon := newDaemonDouble(t, hub)
			client := newOperatorSDK(t, daemon)
			image := foreignHost(t, foreign) + "/x:1"
			err := client.PullImage(context.Background(), image)
			if foreign.sawOperator() {
				t.Fatalf("pull of %s presented operator credentials to the foreign registry (%s challenge); pull error: %v", image, mode, err)
			}
		})
	}
}

func TestPullImageDoesNotPresentOperatorCredentialsWhenRepositoryPathLooksTrusted(t *testing.T) {
	foreign := newRegistryDouble(t, "basic")
	hub := newRegistryDouble(t, "basic")
	daemon := newDaemonDouble(t, hub)
	client := newOperatorSDK(t, daemon)
	image := foreignHost(t, foreign) + "/docker.io/heartwisehub/model:1.2.3"
	err := client.PullImage(context.Background(), image)
	if foreign.sawOperator() {
		t.Fatalf("pull of %s presented operator credentials to the foreign registry; pull error: %v", image, err)
	}
	if hub.sawOperator() {
		t.Fatal("pull of a foreign registry image presented operator credentials to Docker Hub")
	}
}

func TestPullImagePresentsOperatorCredentialsToDockerHub(t *testing.T) {
	hub := newRegistryDouble(t, "basic")
	daemon := newDaemonDouble(t, hub)
	client := newOperatorSDK(t, daemon)
	err := client.PullImage(context.Background(), "heartwisehub/model:1.2.3")
	if err != nil {
		t.Fatalf("docker hub pull: %v", err)
	}
	if !hub.sawOperator() {
		t.Fatal("docker hub pull did not present operator credentials to registry-1.docker.io")
	}
}

func TestAddInferenceModelDoesNotPresentOperatorCredentialsToCallerRegistry(t *testing.T) {
	foreign := newRegistryDouble(t, "basic")
	hub := newRegistryDouble(t, "basic")
	daemon := newDaemonDouble(t, hub)
	client := newOperatorSDK(t, daemon)
	image := foreignHost(t, foreign) + "/x:1"

	command := &service.InferenceCommandService{
		DockerSDKInterface:                  client,
		InferenceCommandRepositoryInterface: insertOnlyRepository{},
	}
	controller := &inferencehttp.InferenceCommandController{InferenceCommandServiceInterface: command}
	body := fmt.Sprintf(`{"name":"foreign-model","dockerImage":%s,"outputMode":%s}`, mustJSON(image), mustJSON(string(entity.OutputModeJSON)))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/inference/model/add", strings.NewReader(body))
	request = request.WithContext(context.WithValue(request.Context(), iamtypes.TenantIDCtx, "tenant-1"))
	controller.AddInferenceModel(httptest.NewRecorder(), request)

	if foreign.sawOperator() {
		t.Fatalf("POST /inference/model/add presented operator credentials to %s", image)
	}
	if hub.sawOperator() {
		t.Fatal("POST /inference/model/add presented operator credentials to Docker Hub for a foreign image")
	}
}

func foreignHost(t *testing.T, reg *registryDouble) string {
	t.Helper()
	parsed, err := url.Parse(reg.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}

func mustJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

type insertOnlyRepository struct {
	domainrepo.InferenceCommandRepositoryInterface
}

func (insertOnlyRepository) InsertInferenceModel(context.Context, repotypes.AddInferenceModel) error {
	return nil
}
