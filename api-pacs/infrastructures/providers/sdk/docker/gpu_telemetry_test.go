package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
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
				state := &container.State{Status: container.ContainerState(status), StartedAt: "2026-01-01T00:00:00Z", FinishedAt: "2026-01-01T00:00:00Z"}
				host := &container.HostConfig{Resources: container.Resources{DeviceRequests: gpuDeviceRequests("")}}
				json.NewEncoder(w).Encode(container.InspectResponse{ID: strings.Repeat("a", 64), Name: "/model", State: state, HostConfig: host})
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

func TestGPUProcessesKeepHostPIDsOfTheExpectedGPU(t *testing.T) {
	other := "GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7"
	value := "4188440, " + testGPUUUID + ", 3300\n17, " + other + ", 900\n4188441, " + testGPUUUID + ", 1898 MiB\n55, " + testGPUUUID + ", [N/A]\n"
	processes, err := parseGPUProcesses(value, testGPUUUID)
	if err != nil || len(processes) != 3 || processes[0] != (GPUProcess{PID: "4188440", UsedMemoryMiB: 3300}) || processes[1].UsedMemoryMiB != 1898 || processes[2] != (GPUProcess{PID: "55"}) {
		t.Fatalf("incorrect process attribution input: %+v %v", processes, err)
	}
	if processes, err = parseGPUProcesses("", testGPUUUID); err != nil || len(processes) != 0 {
		t.Fatalf("an idle GPU must be an empty sample: %+v %v", processes, err)
	}
	for _, value := range []string{"abc, " + testGPUUUID + ", 10", "12, " + testGPUUUID + ", -4", "12, " + testGPUUUID, "0, " + testGPUUUID + ", 10"} {
		if _, err := parseGPUProcesses(value, testGPUUUID); err == nil {
			t.Fatalf("invalid process telemetry accepted: %q", value)
		}
	}
}

func TestNvidiaSMIRunsUnderAKillTimeoutAndFallsBackWhenTimeoutIsMissing(t *testing.T) {
	command := []string{"/usr/bin/nvidia-smi", "--query"}
	wrapped := append([]string{"timeout", "-s", "KILL", "2"}, command...)
	var calls [][]string
	run := func(result map[string][2]any) func([]string) (string, int, error) {
		return func(command []string) (string, int, error) {
			calls = append(calls, command)
			outcome := result[command[0]]
			err, _ := outcome[1].(error)
			return "out", outcome[0].(int), err
		}
	}

	// The normal case: one wrapped execution.
	output, err := runWithKillTimeout("ok", command, run(map[string][2]any{"timeout": {0, nil}}))
	if err != nil || output != "out" || !reflect.DeepEqual(calls, [][]string{wrapped}) {
		t.Fatalf("output=%q err=%v calls=%v", output, err, calls)
	}
	// A stuck driver is killed inside the container and reported as a timeout;
	// the bare command is not tried, which would leave a second stuck process.
	calls = nil
	if _, err = runWithKillTimeout("stuck", command, run(map[string][2]any{"timeout": {137, nil}})); !errors.Is(err, ErrGPUProbeTimeout) || len(calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, calls)
	}
	// nvidia-smi failing under timeout is a failed probe, not a missing timeout.
	calls = nil
	if _, err = runWithKillTimeout("failing", command, run(map[string][2]any{"timeout": {9, nil}})); err == nil || errors.Is(err, ErrGPUProbeTimeout) || len(calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, calls)
	}
	// An image without timeout: fall back once, then remember it.
	for name, missing := range map[string][2]any{"status": {127, nil}, "runtime": {0, errors.New(`exec: "timeout": executable file not found in $PATH`)}} {
		calls = nil
		outcomes := map[string][2]any{"timeout": missing, "/usr/bin/nvidia-smi": {0, nil}}
		if output, err = runWithKillTimeout("bare-"+name, command, run(outcomes)); err != nil || output != "out" || !reflect.DeepEqual(calls, [][]string{wrapped, command}) {
			t.Fatalf("%s: output=%q err=%v calls=%v", name, output, err, calls)
		}
		calls = nil
		if _, err = runWithKillTimeout("bare-"+name, command, run(outcomes)); err != nil || !reflect.DeepEqual(calls, [][]string{command}) {
			t.Fatalf("%s: the missing timeout was probed again: %v %v", name, err, calls)
		}
	}
}
