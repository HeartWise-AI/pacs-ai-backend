package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	dockertypes "api-pacs/infrastructures/providers/sdk/docker/types"
)

func TestCandidateContainerConfigsPreserveRuntimeWithoutIdentityConflicts(t *testing.T) {
	active := container.InspectResponse{
		HostConfig: &container.HostConfig{
			Binds: []string{"model-data:/models:ro"},
			Mounts: []mount.Mount{{
				Type:   mount.TypeBind,
				Source: "/srv/cache",
				Target: "/cache",
			}},
			ShmSize: 4096,
			Runtime: "nvidia",
			Resources: container.Resources{
				Memory:         8192,
				DeviceRequests: []container.DeviceRequest{{Driver: "nvidia", Count: -1}},
			},
			DNS:             []netip.Addr{netip.IPv4Unspecified()},
			PortBindings:    network.PortMap{network.MustParsePort("80/tcp"): []network.PortBinding{{HostPort: "8080"}}},
			PublishAllPorts: true,
			Links:           []string{"database:database"},
			AutoRemove:      true,
			ContainerIDFile: "/tmp/active.cid",
			RestartPolicy:   container.RestartPolicy{Name: container.RestartPolicyAlways},
		},
		Config: &container.Config{
			Env:          []string{"ACTIVE_IMAGE_DEFAULT=old"},
			ExposedPorts: network.PortSet{network.MustParsePort("80/tcp"): struct{}{}},
		},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"pacs-net": {IPAddress: netip.MustParseAddr("172.20.0.5"), Aliases: []string{"active-model"}},
			"metrics":  {IPAddress: netip.MustParseAddr("172.21.0.5"), Aliases: []string{"active-metrics"}},
		}},
	}

	sdk := &DockerSDK{Network: "fallback-net"}
	containerConfig, hostConfig, networkConfig := sdk.candidateContainerConfigs(dockertypes.CreateCandidateContainer{
		Image: "heartwise/model:2.0.0",
		Envs:  []string{"REGISTRATION_SETTING=current"},
	}, active)

	if containerConfig.Image != "heartwise/model:2.0.0" {
		t.Fatalf("candidate image = %q", containerConfig.Image)
	}
	if !slices.Equal(containerConfig.Env, []string{"REGISTRATION_SETTING=current"}) {
		t.Fatalf("candidate environment = %v", containerConfig.Env)
	}
	if _, ok := containerConfig.ExposedPorts[network.MustParsePort("80/tcp")]; !ok {
		t.Fatalf("candidate exposed ports = %v", containerConfig.ExposedPorts)
	}
	if hostConfig.ShmSize != 4096 || hostConfig.Runtime != "nvidia" || hostConfig.Resources.Memory != 8192 {
		t.Fatalf("runtime requirements were not preserved: %+v", hostConfig)
	}
	if len(hostConfig.DeviceRequests) != 1 || hostConfig.DeviceRequests[0].Driver != "nvidia" {
		t.Fatalf("GPU requests were not preserved: %v", hostConfig.DeviceRequests)
	}
	if !slices.Equal(hostConfig.Binds, []string{"model-data:/models:ro"}) || len(hostConfig.Mounts) != 1 {
		t.Fatalf("mounts were not preserved: binds=%v mounts=%v", hostConfig.Binds, hostConfig.Mounts)
	}
	if !slices.Equal(hostConfig.DNS, []netip.Addr{netip.IPv4Unspecified()}) {
		t.Fatalf("network restrictions were not preserved: %v", hostConfig.DNS)
	}
	if hostConfig.PortBindings != nil || hostConfig.PublishAllPorts || hostConfig.Links != nil || hostConfig.AutoRemove || hostConfig.ContainerIDFile != "" {
		t.Fatalf("active identity settings leaked into candidate: %+v", hostConfig)
	}
	if hostConfig.RestartPolicy.Name != container.RestartPolicyDisabled {
		t.Fatalf("candidate restart policy = %q", hostConfig.RestartPolicy.Name)
	}
	if len(networkConfig.EndpointsConfig) != 2 {
		t.Fatalf("candidate network memberships = %v", networkConfig.EndpointsConfig)
	}
	for name, endpoint := range networkConfig.EndpointsConfig {
		if endpoint.IPAddress.IsValid() || len(endpoint.Aliases) != 0 {
			t.Fatalf("network %s retained active identity: %+v", name, endpoint)
		}
	}

	if active.HostConfig.PortBindings == nil || !active.HostConfig.PublishAllPorts || !active.HostConfig.AutoRemove {
		t.Fatal("building candidate configuration mutated the active configuration")
	}
}

func TestCandidateContainerConfigsUseConfiguredNetworkAsFallback(t *testing.T) {
	sdk := &DockerSDK{Network: "pacs-net"}
	_, _, networkConfig := sdk.candidateContainerConfigs(
		dockertypes.CreateCandidateContainer{Image: "heartwise/model:2.0.0"},
		container.InspectResponse{},
	)

	if _, ok := networkConfig.EndpointsConfig["pacs-net"]; !ok || len(networkConfig.EndpointsConfig) != 1 {
		t.Fatalf("candidate fallback networks = %v", networkConfig.EndpointsConfig)
	}
}

func TestCreateCandidateContainerUsesActiveContainerAsTemplate(t *testing.T) {
	var createdName string
	var request struct {
		container.Config
		HostConfig       *container.HostConfig     `json:"HostConfig"`
		NetworkingConfig *network.NetworkingConfig `json:"NetworkingConfig"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/active/json"):
			json.NewEncoder(w).Encode(container.InspectResponse{
				ID:         "active-id",
				Name:       "/active-model",
				HostConfig: &container.HostConfig{ShmSize: 4096},
				Config:     &container.Config{ExposedPorts: network.PortSet{network.MustParsePort("80/tcp"): struct{}{}}},
				NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
					"pacs-net": {},
				}},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			createdName = r.URL.Query().Get("name")
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{"Id": "candidate-id", "Warnings": []string{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	id, err := (&DockerSDK{Client: client}).CreateCandidateContainer(context.Background(), dockertypes.CreateCandidateContainer{
		Name:              "active-model-candidate-upgrade-123",
		Image:             "heartwise/model:2.0.0",
		Envs:              []string{"SETTING=current"},
		ActiveContainerID: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "candidate-id" || createdName != "active-model-candidate-upgrade-123" {
		t.Fatalf("candidate id=%q name=%q", id, createdName)
	}
	if request.Image != "heartwise/model:2.0.0" || !slices.Equal(request.Env, []string{"SETTING=current"}) {
		t.Fatalf("candidate create request = %+v", request.Config)
	}
	if request.HostConfig == nil || request.HostConfig.ShmSize != 4096 {
		t.Fatalf("candidate host config = %+v", request.HostConfig)
	}
	if request.NetworkingConfig == nil || request.NetworkingConfig.EndpointsConfig["pacs-net"] == nil {
		t.Fatalf("candidate networks = %+v", request.NetworkingConfig)
	}
}

func TestCreateCandidateContainerRejectsIncompleteIdentity(t *testing.T) {
	sdk := &DockerSDK{}
	tests := []dockertypes.CreateCandidateContainer{
		{Image: "heartwise/model:2.0.0", ActiveContainerID: "active"},
		{Name: "candidate", ActiveContainerID: "active"},
		{Name: "candidate", Image: "heartwise/model:2.0.0"},
	}
	for _, test := range tests {
		if _, err := sdk.CreateCandidateContainer(context.Background(), test); err == nil {
			t.Fatalf("expected incomplete request to fail: %+v", test)
		}
	}
}

func TestInspectImageReturnsOnlyCandidateIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"Id":          "sha256:local-image-id",
			"RepoDigests": []string{"heartwise/model@sha256:registry-digest"},
			"Config": map[string]any{"Labels": map[string]string{
				"org.opencontainers.image.version": "2.0.0",
			}},
		})
	}))
	defer server.Close()

	client, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	result, err := (&DockerSDK{Client: client}).InspectImage(context.Background(), "heartwise/model:2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "sha256:local-image-id" || !slices.Equal(result.RepoDigests, []string{"heartwise/model@sha256:registry-digest"}) {
		t.Fatalf("image identity = %+v", result)
	}
	if result.Labels["org.opencontainers.image.version"] != "2.0.0" {
		t.Fatalf("image labels = %v", result.Labels)
	}
}

func TestPullImageReturnsRegistryStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"status\":\"Pulling manifest\"}\n"))
		w.Write([]byte("{\"errorDetail\":{\"message\":\"manifest unknown\"},\"error\":\"manifest unknown\"}\n"))
	}))
	defer server.Close()

	client, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	err = (&DockerSDK{Client: client}).PullImage(context.Background(), "heartwise/model:missing")
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("pull error = %v", err)
	}
}

func TestConsumeImagePullAcceptsCompleteProgressStream(t *testing.T) {
	stream := strings.NewReader("{\"status\":\"Pulling\"}\n{\"status\":\"Digest: sha256:abc\"}\n")
	if err := consumeImagePull(stream); err != nil {
		t.Fatalf("consume pull stream: %v", err)
	}
}

func TestRemoveContainerIsIdempotentWhenCandidateIsAlreadyGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"message": "No such container"})
	}))
	defer server.Close()

	client, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := (&DockerSDK{Client: client}).RemoveContainer(context.Background(), "missing-candidate"); err != nil {
		t.Fatalf("idempotent remove returned %v", err)
	}
}

var _ dockertypes.CandidateDockerSDKInterface = (*DockerSDK)(nil)

func TestMovedContainerKeepsEverythingButItsGPU(t *testing.T) {
	const otherGPU = "GPU-22222222-2222-2222-2222-222222222222"
	activeID := strings.Repeat("ab", 32)
	active := container.InspectResponse{
		ID: activeID, Name: "/DeepRV-CLIP", Image: "sha256:running",
		HostConfig: &container.HostConfig{
			Binds:         []string{"model-data:/models:ro"},
			Mounts:        []mount.Mount{{Type: mount.TypeBind, Source: "/srv/cache", Target: "/cache"}},
			ShmSize:       2 << 30,
			Runtime:       "nvidia",
			DNS:           []netip.Addr{netip.IPv4Unspecified()},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			PortBindings:  network.PortMap{network.MustParsePort("80/tcp"): []network.PortBinding{{HostPort: "8080"}}},
			Resources: container.Resources{DeviceRequests: []container.DeviceRequest{{
				Driver: "nvidia", DeviceIDs: []string{"GPU-11111111-1111-1111-1111-111111111111"}, Capabilities: [][]string{{"gpu"}},
			}}},
		},
		Config: &container.Config{
			Image:        "deeprv-clip-local:1.0.1-render-report-1",
			Hostname:     activeID[:12],
			Env:          []string{"PATH=/usr/bin", "MODEL_SETTING=local"},
			Cmd:          []string{"/usr/bin/supervisord", "-c", "/etc/supervisord.conf"},
			Entrypoint:   []string{"/entry.sh"},
			WorkingDir:   "/app",
			User:         "model",
			Labels:       map[string]string{"ai.heartwise.pacs.deployment": "render-report-1", gpuPlacementNameLabel: "DeepRV-CLIP"},
			ExposedPorts: network.PortSet{network.MustParsePort("80/tcp"): struct{}{}},
		},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"pacs-net": {IPAddress: netip.MustParseAddr("172.20.0.5"), Aliases: []string{"DeepRV-CLIP", activeID[:12], "deeprv"}},
		}},
	}

	sdk := &DockerSDK{GPUDefaultUUID: "GPU-11111111-1111-1111-1111-111111111111"}
	containerConfig, hostConfig, networkConfig := sdk.candidateContainerConfigs(dockertypes.CreateCandidateContainer{
		Image: "deeprv-clip-local:1.0.1-render-report-1", GPUUUID: otherGPU,
		Envs:                    []string{"REGISTRATION_SETTING=stale"},
		KeepActiveConfiguration: true,
	}, active)

	if containerConfig.Image != "deeprv-clip-local:1.0.1-render-report-1" {
		t.Fatalf("image = %q", containerConfig.Image)
	}
	if !slices.Equal(containerConfig.Env, active.Config.Env) {
		t.Fatalf("environment was not kept: %v", containerConfig.Env)
	}
	if !slices.Equal(containerConfig.Cmd, active.Config.Cmd) || !slices.Equal(containerConfig.Entrypoint, active.Config.Entrypoint) ||
		containerConfig.WorkingDir != "/app" || containerConfig.User != "model" {
		t.Fatalf("command was not kept: %+v", containerConfig)
	}
	if containerConfig.Labels["ai.heartwise.pacs.deployment"] != "render-report-1" || containerConfig.Labels[gpuPlacementNameLabel] != "DeepRV-CLIP" {
		t.Fatalf("labels were not kept: %v", containerConfig.Labels)
	}
	if containerConfig.Hostname != "" {
		t.Fatalf("the old container's generated hostname leaked: %q", containerConfig.Hostname)
	}
	if hostConfig.RestartPolicy.Name != container.RestartPolicyUnlessStopped || hostConfig.ShmSize != 2<<30 ||
		!slices.Equal(hostConfig.Binds, active.HostConfig.Binds) || len(hostConfig.Mounts) != 1 || !slices.Equal(hostConfig.DNS, []netip.Addr{netip.IPv4Unspecified()}) {
		t.Fatalf("host configuration was not kept: %+v", hostConfig)
	}
	if len(hostConfig.DeviceRequests) != 1 || !slices.Equal(hostConfig.DeviceRequests[0].DeviceIDs, []string{otherGPU}) {
		t.Fatalf("the GPU request is the one thing a move changes: %+v", hostConfig.DeviceRequests)
	}
	if hostConfig.PortBindings != nil {
		t.Fatal("host ports are still held by the active container and cannot be bound twice")
	}
	if aliases := networkConfig.EndpointsConfig["pacs-net"].Aliases; !slices.Equal(aliases, []string{"deeprv"}) {
		t.Fatalf("only operator aliases are kept, never the active container's own name or ID: %v", aliases)
	}

	// The active container's inspection must not be modified.
	containerConfig.Labels["changed"] = "yes"
	containerConfig.Env[0] = "changed"
	if _, leaked := active.Config.Labels["changed"]; leaked || active.Config.Env[0] != "PATH=/usr/bin" {
		t.Fatal("building the moved configuration mutated the active container's")
	}
}

func TestMoveIsNotCreatedWhenTheActiveContainerRunsAnotherImage(t *testing.T) {
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/active/json"):
			json.NewEncoder(w).Encode(container.InspectResponse{
				ID: "active-id", Name: "/active-model", Image: "sha256:replaced", HostConfig: &container.HostConfig{},
				Config: &container.Config{},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			created = true
			json.NewEncoder(w).Encode(map[string]any{"Id": "candidate-id"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = (&DockerSDK{Client: client}).CreateCandidateContainer(context.Background(), dockertypes.CreateCandidateContainer{
		Name: "active-model-candidate-1", Image: "model-local:1", ActiveContainerID: "active",
		KeepActiveConfiguration: true, ExpectedImageID: "sha256:running",
	})
	if err == nil || created {
		t.Fatalf("a move from a container whose image changed must be refused: err=%v created=%v", err, created)
	}
}
