package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	engine "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"

	"api-pacs/infrastructures/providers/sdk/docker/types"
)

// DockerSDK docker sdk
type DockerSDK struct {
	Client  *client.Client
	AuthStr string
	Network string
}

// NewClient creates a new docker client instance
func NewClient(config types.Config) (*DockerSDK, error) {
	client, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	// docker registry credentials
	authConfig := registry.AuthConfig{
		Username: config.Username,
		Password: config.Password,
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		log.Println("[docker] error:", err)
		return nil, err
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	return &DockerSDK{
		Client:  client,
		AuthStr: authStr,
		Network: config.Network,
	}, nil
}

// CreateContainer creates a new container
func (d *DockerSDK) CreateContainer(ctx context.Context, config types.CreateContainer) (string, error) {
	// define container config
	containerConfig := &container.Config{
		Image: config.Image,
		Env:   config.Envs,
		ExposedPorts: nat.PortSet{
			"80/tcp": struct{}{}, // expose port 80 (nginx)
		},
	}

	// define host config
	hostConfig := &container.HostConfig{
		ShmSize: 2 * 1024 * 1024 * 1024, // 2GB in bytes
	}

	// try to use NVIDIA runtime if available
	isNvidiaSupported, err := d.checkNvidiaRuntime(ctx)
	if err != nil {
		return "", err
	}

	if isNvidiaSupported {
		hostConfig.Runtime = "nvidia"
		// use all GPUs
		hostConfig.Resources = container.Resources{
			DeviceRequests: []container.DeviceRequest{
				{
					Driver:       "nvidia",
					Count:        -1, // Use all available GPUs
					Capabilities: [][]string{{"gpu"}},
				},
			},
		}
	} else {
		log.Println("[docker] NVIDIA runtime not available")
	}

	// define network config
	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			d.Network: {}, // external network
		},
	}

	// override with DNS settings to block internet access
	hostConfig.DNS = []string{"0.0.0.0"} // invalid DNS to prevent resolution
	hostConfig.DNSSearch = []string{""}  // empty DNS search

	// create container
	resp, err := d.Client.ContainerCreate(ctx, containerConfig, hostConfig, networkConfig, nil, config.Name)
	if err != nil {
		log.Println("[docker] error:", err)
		return "", err
	}

	return resp.ID, nil
}

// CreateCandidateContainer stages a uniquely named container beside the active
// deployment. It preserves runtime requirements while removing settings that
// could claim the active container's identity or host ports.
func (d *DockerSDK) CreateCandidateContainer(ctx context.Context, config types.CreateCandidateContainer) (string, error) {
	config.Name = strings.TrimSpace(config.Name)
	config.Image = strings.TrimSpace(config.Image)
	config.ActiveContainerID = strings.TrimSpace(config.ActiveContainerID)
	if config.Name == "" || config.Image == "" || config.ActiveContainerID == "" {
		return "", fmt.Errorf("candidate name, image, and active container ID are required")
	}

	active, err := d.Client.ContainerInspect(ctx, config.ActiveContainerID)
	if err != nil {
		log.Println("[docker] error:", err)
		return "", err
	}
	if active.ContainerJSONBase == nil || active.Config == nil || active.HostConfig == nil {
		return "", fmt.Errorf("active container inspection is incomplete")
	}
	if strings.TrimPrefix(active.Name, "/") == config.Name {
		return "", fmt.Errorf("candidate container name must differ from the active container name")
	}

	containerConfig, hostConfig, networkConfig := d.candidateContainerConfigs(config, active)
	response, err := d.Client.ContainerCreate(ctx, containerConfig, hostConfig, networkConfig, nil, config.Name)
	if err != nil {
		log.Println("[docker] error:", err)
		return "", err
	}

	return response.ID, nil
}

func (d *DockerSDK) candidateContainerConfigs(
	config types.CreateCandidateContainer,
	active engine.ContainerJSON,
) (*container.Config, *container.HostConfig, *network.NetworkingConfig) {
	containerConfig := &container.Config{
		Image: config.Image,
		Env:   append([]string(nil), config.Envs...),
	}
	if active.Config != nil {
		containerConfig.ExposedPorts = clonePortSet(active.Config.ExposedPorts)
	}

	hostConfig := &container.HostConfig{}
	if active.ContainerJSONBase != nil && active.HostConfig != nil {
		copied := *active.HostConfig
		copied.Binds = append([]string(nil), active.HostConfig.Binds...)
		copied.Mounts = append([]mount.Mount(nil), active.HostConfig.Mounts...)
		copied.DeviceRequests = append([]container.DeviceRequest(nil), active.HostConfig.DeviceRequests...)
		copied.DNS = append([]string(nil), active.HostConfig.DNS...)
		copied.DNSSearch = append([]string(nil), active.HostConfig.DNSSearch...)
		copied.DNSOptions = append([]string(nil), active.HostConfig.DNSOptions...)

		// A candidate shares runtime requirements, but not the active
		// deployment's externally visible identity or lifecycle policy.
		copied.PortBindings = nil
		copied.PublishAllPorts = false
		copied.Links = nil
		copied.AutoRemove = false
		copied.ContainerIDFile = ""
		copied.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
		hostConfig = &copied
	}

	networkConfig := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{}}
	if active.NetworkSettings != nil {
		for networkName := range active.NetworkSettings.Networks {
			networkConfig.EndpointsConfig[networkName] = &network.EndpointSettings{}
		}
	}
	if len(networkConfig.EndpointsConfig) == 0 && strings.TrimSpace(d.Network) != "" {
		networkConfig.EndpointsConfig[d.Network] = &network.EndpointSettings{}
	}

	return containerConfig, hostConfig, networkConfig
}

func clonePortSet(source nat.PortSet) nat.PortSet {
	if source == nil {
		return nil
	}
	cloned := make(nat.PortSet, len(source))
	for port := range source {
		cloned[port] = struct{}{}
	}
	return cloned
}

// GetContainerInfo gets the container info
func (d *DockerSDK) GetContainerInfo(ctx context.Context, containerID string) (types.GetContainerInfoResult, error) {

	// inspect the container for basic info
	containerJSON, err := d.Client.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Println("[docker] error:", err)
		return types.GetContainerInfoResult{}, err
	}

	// convert to time
	startedAtTime, err := time.Parse(time.RFC3339Nano, containerJSON.State.StartedAt)
	if err != nil {
		log.Println("[docker] error:", err)
		return types.GetContainerInfoResult{}, err
	}

	finishedAtTime, err := time.Parse(time.RFC3339Nano, containerJSON.State.FinishedAt)
	if err != nil {
		log.Println("[docker] error:", err)
		return types.GetContainerInfoResult{}, err
	}

	return types.GetContainerInfoResult{
		ID:         containerJSON.ID,
		Name:       containerJSON.Name,
		Lifecycle:  types.LifecycleFromStatus(types.Status(containerJSON.State.Status)),
		StartedAt:  startedAtTime,
		FinishedAt: finishedAtTime,
	}, nil
}

// PullImage pulls an image from registry
func (d *DockerSDK) PullImage(ctx context.Context, imageName string) error {
	reader, err := d.Client.ImagePull(ctx, imageName, image.PullOptions{
		RegistryAuth: d.AuthStr,
	})
	if err != nil {
		log.Println("[docker] error:", err)
		return err
	}
	defer reader.Close()
	// cli.ImagePull is asynchronous.
	// The reader needs to be read completely for the pull operation to complete.
	if err = consumeImagePull(reader); err != nil {
		log.Println("[docker] error:", err)
		return err
	}

	return nil
}

func consumeImagePull(reader io.Reader) error {
	decoder := json.NewDecoder(reader)
	for {
		var message struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read image pull response: %w", err)
		}
		if message.ErrorDetail.Message != "" {
			return fmt.Errorf("image pull failed: %s", message.ErrorDetail.Message)
		}
		if message.Error != "" {
			return fmt.Errorf("image pull failed: %s", message.Error)
		}
	}
}

// InspectImage returns the local immutable identity, repository digests, and
// OCI labels used to validate a candidate image.
func (d *DockerSDK) InspectImage(ctx context.Context, imageName string) (types.InspectImageResult, error) {
	inspection, _, err := d.Client.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		log.Println("[docker] error:", err)
		return types.InspectImageResult{}, err
	}

	labels := map[string]string{}
	if inspection.Config != nil {
		for key, value := range inspection.Config.Labels {
			labels[key] = value
		}
	}

	return types.InspectImageResult{
		ID:          inspection.ID,
		RepoDigests: append([]string(nil), inspection.RepoDigests...),
		Labels:      labels,
	}, nil
}

// RemoveContainer removes a container
func (d *DockerSDK) RemoveContainer(ctx context.Context, containerID string) error {
	err := d.Client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		log.Println("[docker] error:", err)
		return err
	}

	return nil
}

// RestartContainer restarts a container
func (d *DockerSDK) RestartContainer(ctx context.Context, containerID string) error {
	err := d.Client.ContainerRestart(ctx, containerID, container.StopOptions{})
	if err != nil {
		log.Println("[docker] error:", err)
		return err
	}

	return nil
}

// StartContainer starts a container
func (d *DockerSDK) StartContainer(ctx context.Context, containerID string) error {
	err := d.Client.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		log.Println("[docker] error:", err)
		return err
	}

	// TODO: used to run container synchronously (wait)
	// wait for container to start
	// statusCh, errCh := d.Client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	// select {
	// case err := <-errCh:
	// 	if err != nil {
	// 		log.Println("[docker] error:", err)
	// 		return err
	// 	}
	// case <-statusCh:
	// }

	// TODO: ability to check container logs?
	// https://docs.docker.com/reference/api/engine/sdk/examples/#list-and-manage-containers

	return nil
}

// StopContainer stops a container
func (d *DockerSDK) StopContainer(ctx context.Context, containerID string) error {
	err := d.Client.ContainerStop(ctx, containerID, container.StopOptions{})
	if err != nil {
		log.Println("[docker] error:", err)
		return err
	}

	return nil
}

func (d *DockerSDK) checkNvidiaRuntime(ctx context.Context) (bool, error) {
	info, err := d.Client.Info(ctx)
	if err != nil {
		log.Println("[docker] error:", err)
		return false, err
	}

	// check if 'nvidia' is in the list of available runtimes
	for runtime := range info.Runtimes {
		if strings.EqualFold(runtime, "nvidia") {
			return true, nil
		}
	}

	return false, nil
}
