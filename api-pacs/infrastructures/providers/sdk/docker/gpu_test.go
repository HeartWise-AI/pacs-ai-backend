package docker

import (
	dockertypes "api-pacs/infrastructures/providers/sdk/docker/types"
	engine "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"slices"
	"testing"
)

const testGPUUUID = "GPU-999e1fca-539d-fb99-2531-ad97b91da026"

func TestGPUPlacementUsesUUIDAndRejectsAmbiguousInspection(t *testing.T) {
	r := gpuDeviceRequests(testGPUUUID)
	if r[0].Count != 0 || !slices.Equal(r[0].DeviceIDs, []string{testGPUUUID}) {
		t.Fatal(r)
	}
	if got := singleGPUUUID(&container.HostConfig{Resources: container.Resources{DeviceRequests: r}}); got != testGPUUUID {
		t.Fatal(got)
	}
	for _, requests := range [][]container.DeviceRequest{
		gpuDeviceRequests(""),
		{{Driver: "nvidia", DeviceIDs: []string{"0"}}},
		{{Driver: "nvidia", DeviceIDs: []string{testGPUUUID, testGPUUUID}}},
	} {
		if singleGPUUUID(&container.HostConfig{Resources: container.Resources{DeviceRequests: requests}}) != "" {
			t.Fatal("guessed physical GPU from ambiguous configuration")
		}
	}
}

func TestUpgradeUsesActiveModelGPUAssignment(t *testing.T) {
	other := "GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7"
	sdk := &DockerSDK{GPUDefaultUUID: other, GPUAssignments: map[string]string{"deepcoro-jepa": testGPUUUID}}
	active := engine.ContainerJSON{ContainerJSONBase: &engine.ContainerJSONBase{
		Name: "/deepcoro-jepa", HostConfig: &container.HostConfig{Resources: container.Resources{DeviceRequests: gpuDeviceRequests(other)}},
	}}
	_, host, _ := sdk.candidateContainerConfigs(dockertypes.CreateCandidateContainer{Name: "temporary-candidate"}, active)
	if singleGPUUUID(host) != testGPUUUID {
		t.Fatal("candidate lost the active model's placement policy", host.DeviceRequests)
	}
	if singleGPUUUID(active.HostConfig) != other {
		t.Fatal("mutated running container settings")
	}
	if sdk.gpuForContainer("new-model") != other {
		t.Fatal("unlisted model did not receive fallback GPU")
	}
}

func TestGPUAssignmentsRequireUUIDAndFallback(t *testing.T) {
	for _, tc := range []struct{ fallback, raw string }{
		{"0", ""},
		{"", `{"deepcoro-jepa":"` + testGPUUUID + `"}`},
		{testGPUUUID, `{"deepcoro-jepa":"1"}`},
		{testGPUUUID, `{`},
	} {
		if _, err := parseGPUAssignments(tc.fallback, tc.raw); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	assignments, err := parseGPUAssignments(testGPUUUID, `{"deepcoro-jepa":"`+testGPUUUID+`"}`)
	if err != nil || assignments["deepcoro-jepa"] != testGPUUUID {
		t.Fatal(assignments, err)
	}
}
