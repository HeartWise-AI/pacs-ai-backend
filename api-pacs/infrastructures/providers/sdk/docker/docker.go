package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
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
		log.Fatalf("[docker] error:", err)
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
	hostConfig := &container.HostConfig{}

	// define network config
	networkConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			d.Network: {}, // external network
		},
	}

	// create container
	resp, err := d.Client.ContainerCreate(ctx, containerConfig, hostConfig, networkConfig, nil, config.Name)
	if err != nil {
		log.Println("[docker] error:", err)
		return "", err
	}

	return resp.ID, nil
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

	// fetch real-time stats
	stats, err := d.Client.ContainerStats(ctx, containerID, false)
	if err != nil {
		log.Println("[docker] error:", err)
		return types.GetContainerInfoResult{}, err
	}
	defer stats.Body.Close()

	var statsJSON container.Stats
	if err := json.NewDecoder(stats.Body).Decode(&statsJSON); err != nil {
		log.Println("[docker] error:", err)
		return types.GetContainerInfoResult{}, err
	}

	// calculate cpu usage percentage
	cpuDelta := float64(statsJSON.CPUStats.CPUUsage.TotalUsage - statsJSON.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(statsJSON.CPUStats.SystemUsage - statsJSON.PreCPUStats.SystemUsage)
	onlineCPUs := float64(statsJSON.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(statsJSON.CPUStats.CPUUsage.PercpuUsage)) // Better fallback
		if onlineCPUs == 0 {
			onlineCPUs = 1 // Final fallback to prevent division by zero
		}
	}
	// calculate percentage across all cores
	cpuUsagePercent := 0.0
	if cpuDelta > 0.0 && systemDelta > 0.0 {
		cpuUsagePercent = (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}

	return types.GetContainerInfoResult{
		ID:              containerJSON.ID,
		Name:            containerJSON.Name,
		Status:          types.Status(containerJSON.State.Status),
		Running:         containerJSON.State.Running,
		StartedAt:       startedAtTime,
		FinishedAt:      finishedAtTime,
		CPUPercentUsage: cpuUsagePercent,
		MemoryInBytes:   statsJSON.MemoryStats.Usage,
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
	// If stdout is not required, consider using io.Discard instead of os.Stdout.
	io.Copy(io.Discard, reader)

	return nil
}

// RemoveContainer removes a container
func (d *DockerSDK) RemoveContainer(ctx context.Context, containerID string) error {
	err := d.Client.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: true,
		Force:         true,
	})
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
