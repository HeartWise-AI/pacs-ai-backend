package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/modelupgrade"
)

type lifecycleDocker struct {
	dockerTypes.DockerSDKInterface
	calls []string
}

func (docker *lifecycleDocker) StartContainer(_ context.Context, id string) error {
	docker.calls = append(docker.calls, "start:"+id)
	return nil
}

func (docker *lifecycleDocker) StopContainer(_ context.Context, id string) error {
	docker.calls = append(docker.calls, "stop:"+id)
	return nil
}

func (docker *lifecycleDocker) RestartContainer(_ context.Context, id string) error {
	docker.calls = append(docker.calls, "restart:"+id)
	return nil
}

type lifecycleDeployments struct {
	active bool
	err    error
}

func (deployments *lifecycleDeployments) Start(context.Context, modelupgrade.StartDeploymentOperation) (entity.InferenceModelDeploymentOperation, error) {
	return entity.InferenceModelDeploymentOperation{}, nil
}

func (deployments *lifecycleDeployments) Get(context.Context, string, string, string) (entity.InferenceModelDeploymentOperation, error) {
	return entity.InferenceModelDeploymentOperation{}, nil
}

func (deployments *lifecycleDeployments) HasActiveDeployment(context.Context) (bool, error) {
	return deployments.active, deployments.err
}

// Manual start, stop and restart must not run during a deployment, which
// swaps containers on the same host (Codex review on #366).
func TestContainerLifecycleControlsWaitForRunningDeployments(t *testing.T) {
	controls := map[string]func(*InferenceCommandService) error{
		"start": func(service *InferenceCommandService) error {
			return service.StartInferenceModelContainer(context.Background(), "c1")
		},
		"stop": func(service *InferenceCommandService) error {
			return service.StopInferenceModelContainer(context.Background(), "c1")
		},
		"restart": func(service *InferenceCommandService) error {
			return service.RestartInferenceModelContainer(context.Background(), "c1")
		},
	}
	for name, control := range controls {
		t.Run(name, func(t *testing.T) {
			docker := &lifecycleDocker{}
			service := &InferenceCommandService{DockerSDKInterface: docker, ModelDeploymentOperations: &lifecycleDeployments{active: true}}
			require.EqualError(t, control(service), apiError.InferenceUpgradeConflict)
			require.Empty(t, docker.calls)

			service.ModelDeploymentOperations = &lifecycleDeployments{err: errors.New("firestore down")}
			require.EqualError(t, control(service), apiError.InferenceUpgradeUnavailable)
			require.Empty(t, docker.calls)

			service.ModelDeploymentOperations = &lifecycleDeployments{}
			require.NoError(t, control(service))
			require.Equal(t, []string{name + ":c1"}, docker.calls)
		})
	}
}
