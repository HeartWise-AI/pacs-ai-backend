package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	engine "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func TestGPUTelemetryParsesOnlyExpectedPhysicalUUID(t *testing.T) {
	other := "GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7"
	at := time.Now()
	value := other + ", 0, Other, 24000, 100, 23900, 0\n" + testGPUUUID + ", 1, NVIDIA A5000, 24564, 15000, 9564, 80\n"
	measured, err := parseGPUTelemetry(value, testGPUUUID, at)
	if err != nil || measured.UUID != testGPUUUID || measured.UsedMemoryMiB != 15000 || measured.TotalMemoryMiB != 24564 || measured.UtilizationPercent != 80 || measured.SampledAt != at {
		t.Fatalf("incorrect physical telemetry: %+v %v", measured, err)
	}
	for _, value := range []string{"", testGPUUUID + ", 0, GPU, 24, 25, 0, 0", testGPUUUID + ", 0, GPU, 24, 0, 24, 101", testGPUUUID + ", 0, GPU, N/A, 0, 24, 0"} {
		if _, err := parseGPUTelemetry(value, testGPUUUID, at); err == nil {
			t.Fatalf("invalid telemetry accepted: %q", value)
		}
	}
}
func TestGPUTelemetryNeverStartsStoppedOrWrongGPUProbe(t *testing.T) {
	for _, status := range []string{"exited", "running"} {
		t.Run(status, func(t *testing.T) {
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations++
					http.Error(w, "unexpected mutation", 500)
					return
				}
				state := &engine.ContainerState{Status: status, StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:00:00Z"}
				host := &container.HostConfig{Resources: container.Resources{DeviceRequests: gpuDeviceRequests("")}}
				json.NewEncoder(w).Encode(engine.ContainerJSON{ContainerJSONBase: &engine.ContainerJSONBase{ID: strings.Repeat("a", 64), Name: "/model", State: state, HostConfig: host}})
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			_, err = (&DockerSDK{Client: cli}).ReadGPUTelemetry(context.Background(), strings.Repeat("a", 64), testGPUUUID)
			if err == nil || mutations != 0 {
				t.Fatalf("unsafe telemetry probe: mutations=%d err=%v", mutations, err)
			}
		})
	}
}
