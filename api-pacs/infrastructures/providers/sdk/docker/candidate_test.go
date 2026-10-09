package docker

import (
	"testing"

	engine "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

func TestApplyCandidateTemplatePreservesRuntimeWithoutPortConflicts(t *testing.T) {
	containerConfig := &container.Config{}
	hostConfig := &container.HostConfig{}
	networkConfig := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{"default": {}}}
	template := engine.ContainerJSON{
		ContainerJSONBase: &engine.ContainerJSONBase{HostConfig: &container.HostConfig{
			Binds: []string{"model-data:/models:ro"}, ShmSize: 4096, Runtime: "nvidia",
			Resources:       container.Resources{Memory: 8192, DeviceRequests: []container.DeviceRequest{{Driver: "nvidia", Count: -1}}},
			PortBindings:    nat.PortMap{"80/tcp": []nat.PortBinding{{HostPort: "8080"}}},
			PublishAllPorts: true, AutoRemove: true, ContainerIDFile: "/tmp/active.cid",
		}},
		Config: &container.Config{ExposedPorts: nat.PortSet{"80/tcp": {}}},
		NetworkSettings: &engine.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"pacs-net": {IPAddress: "172.20.0.5"},
			"metrics":  {Aliases: []string{"active-model"}},
		}},
	}

	hostConfig = applyCandidateTemplate(containerConfig, hostConfig, networkConfig, template)
	if hostConfig.ShmSize != 4096 || hostConfig.Runtime != "nvidia" || hostConfig.Resources.Memory != 8192 || len(hostConfig.Resources.DeviceRequests) != 1 {
		t.Fatalf("runtime resources were not preserved: %+v", hostConfig)
	}
	if len(hostConfig.Binds) != 1 || hostConfig.Binds[0] != "model-data:/models:ro" {
		t.Fatalf("mounts were not preserved: %v", hostConfig.Binds)
	}
	if hostConfig.PortBindings != nil || hostConfig.PublishAllPorts || hostConfig.AutoRemove || hostConfig.ContainerIDFile != "" {
		t.Fatalf("conflicting host settings were retained: %+v", hostConfig)
	}
	if len(containerConfig.ExposedPorts) != 1 {
		t.Fatalf("container ports were not preserved: %v", containerConfig.ExposedPorts)
	}
	if len(networkConfig.EndpointsConfig) != 2 || networkConfig.EndpointsConfig["pacs-net"].IPAddress != "" || len(networkConfig.EndpointsConfig["metrics"].Aliases) != 0 {
		t.Fatalf("network memberships were not safely cloned: %+v", networkConfig.EndpointsConfig)
	}
}
