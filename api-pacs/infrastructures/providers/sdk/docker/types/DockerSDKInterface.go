package types

import "context"

type DockerSDKInterface interface {
	// CreateContainer creates a container
	CreateContainer(ctx context.Context, config CreateContainer) (string, error)
	// GetContainerInfo gets a container info
	GetContainerInfo(ctx context.Context, containerID string) (GetContainerInfoResult, error)
	// GetContainerStats gets a container stats
	GetContainerStats(ctx context.Context, containerID string) (GetContainerStatsResult, error)
	// PullImage pulls an image
	PullImage(ctx context.Context, imageName string) error
	// RemoveContainer removes a container
	RemoveContainer(ctx context.Context, containerID string) error
	// RestartContainer restarts a container
	RestartContainer(ctx context.Context, containerID string) error
	// StartContainer starts a container
	StartContainer(ctx context.Context, containerID string) error
	// StopContainer stops a container
	StopContainer(ctx context.Context, containerID string) error
}
