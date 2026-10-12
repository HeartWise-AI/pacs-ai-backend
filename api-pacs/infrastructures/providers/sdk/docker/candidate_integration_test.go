package docker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	dockertypes "api-pacs/infrastructures/providers/sdk/docker/types"
)

func TestCandidateContainerDockerIntegration(t *testing.T) {
	activeID := strings.TrimSpace(os.Getenv("PACS_AI_DOCKER_INTEGRATION_ACTIVE_CONTAINER"))
	if activeID == "" {
		t.Skip("set PACS_AI_DOCKER_INTEGRATION_ACTIVE_CONTAINER to run against Docker")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer dockerClient.Close()

	active, err := dockerClient.ContainerInspect(ctx, activeID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Config == nil || active.HostConfig == nil {
		t.Fatal("active container inspection is incomplete")
	}
	if active.Image == "" {
		t.Fatal("active container has no local image identity")
	}

	sdk := &DockerSDK{Client: dockerClient}
	name := fmt.Sprintf("pacs-ai-candidate-integration-%d", time.Now().UnixNano())
	candidateID, err := sdk.CreateCandidateContainer(ctx, dockertypes.CreateCandidateContainer{
		Name:              name,
		Image:             active.Image,
		Envs:              active.Config.Env,
		ActiveContainerID: activeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if cleanupErr := sdk.RemoveContainer(cleanupCtx, candidateID); cleanupErr != nil {
			t.Errorf("remove candidate: %v", cleanupErr)
		}
	})

	candidate, err := dockerClient.ContainerInspect(ctx, candidateID)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Name != "/"+name || candidate.Image != active.Image {
		t.Fatalf("unexpected candidate identity: name=%q image=%q", candidate.Name, candidate.Config.Image)
	}
	if candidate.State == nil || candidate.State.Status != "created" {
		t.Fatalf("candidate was not left stopped: %+v", candidate.State)
	}
	if len(candidate.HostConfig.PortBindings) != 0 || candidate.HostConfig.PublishAllPorts {
		t.Fatalf("candidate claimed host ports: %+v", candidate.HostConfig.PortBindings)
	}
	if candidate.HostConfig.RestartPolicy.Name != container.RestartPolicyDisabled {
		t.Fatalf("candidate restart policy = %q", candidate.HostConfig.RestartPolicy.Name)
	}
	if len(candidate.HostConfig.DeviceRequests) != len(active.HostConfig.DeviceRequests) {
		t.Fatalf("candidate GPU requests = %v, active = %v", candidate.HostConfig.DeviceRequests, active.HostConfig.DeviceRequests)
	}
	if len(candidate.NetworkSettings.Networks) != len(active.NetworkSettings.Networks) {
		t.Fatalf("candidate networks = %v, active = %v", candidate.NetworkSettings.Networks, active.NetworkSettings.Networks)
	}
}
