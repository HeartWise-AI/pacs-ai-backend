package docker

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/docker/docker/api/types/container"
)

var gpuUUIDPattern = regexp.MustCompile(`^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Upgrade candidates retain temporary Docker names after activation. Keep the
// registration's original placement key across every generation.
const gpuPlacementNameLabel = "ai.heartwise.pacs.gpu-placement-name"

func parseGPUAssignments(defaultUUID, raw string) (map[string]string, error) {
	assignments := map[string]string{}
	if defaultUUID != "" && !gpuUUIDPattern.MatchString(defaultUUID) {
		return nil, fmt.Errorf("MODEL_GPU_DEFAULT_UUID must be a physical GPU UUID")
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &assignments); err != nil {
			return nil, fmt.Errorf("MODEL_GPU_ASSIGNMENTS: %w", err)
		}
		if defaultUUID == "" {
			return nil, fmt.Errorf("MODEL_GPU_ASSIGNMENTS requires MODEL_GPU_DEFAULT_UUID for unlisted models")
		}
	}
	for name, uuid := range assignments {
		if name == "" || !gpuUUIDPattern.MatchString(uuid) {
			return nil, fmt.Errorf("MODEL_GPU_ASSIGNMENTS requires container names mapped to physical GPU UUIDs")
		}
	}
	return assignments, nil
}

func gpuDeviceRequests(uuid string) []container.DeviceRequest {
	request := container.DeviceRequest{Driver: "nvidia", Capabilities: [][]string{{"gpu"}}}
	if uuid == "" {
		request.Count = -1
	} else {
		request.DeviceIDs = []string{uuid}
	}
	return []container.DeviceRequest{request}
}

// Return no assignment for legacy all-device or numeric configurations: the
// multi-GPU admission manager must not guess which physical GPU CUDA selected.
func singleGPUUUID(host *container.HostConfig) string {
	if host == nil || len(host.DeviceRequests) != 1 {
		return ""
	}
	r := host.DeviceRequests[0]
	if r.Count != 0 || len(r.DeviceIDs) != 1 || !gpuUUIDPattern.MatchString(r.DeviceIDs[0]) {
		return ""
	}
	return r.DeviceIDs[0]
}

func (d *DockerSDK) gpuForContainer(name string) string {
	if uuid := d.GPUAssignments[name]; uuid != "" {
		return uuid
	}
	return d.GPUDefaultUUID
}
