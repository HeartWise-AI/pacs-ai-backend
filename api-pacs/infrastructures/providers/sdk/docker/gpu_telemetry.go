package docker

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

type GPUTelemetry struct {
	UUID                                                             string
	Index                                                            int
	Name                                                             string
	TotalMemoryMiB, UsedMemoryMiB, FreeMemoryMiB, UtilizationPercent int
	SampledAt                                                        time.Time
}

type boundedTelemetryWriter struct{ bytes.Buffer }

func (writer *boundedTelemetryWriter) Write(data []byte) (int, error) {
	if writer.Len()+len(data) > 16384 {
		return 0, errors.New("GPU telemetry output exceeded limit")
	}
	return writer.Buffer.Write(data)
}

// ReadGPUTelemetry executes only a fixed read-only command inside an existing,
// already-running pinned model. It never starts or creates a helper container.
func (sdk *DockerSDK) ReadGPUTelemetry(ctx context.Context, id, uuid string) (GPUTelemetry, error) {
	output, err := sdk.runNvidiaSMI(ctx, id, uuid, "--query-gpu=uuid,index,name,memory.total,memory.used,memory.free,utilization.gpu")
	if err != nil {
		return GPUTelemetry{}, err
	}
	return parseGPUTelemetry(output, uuid, time.Now().UTC())
}

// GPUProcess is one compute process on a physical GPU. PID is a host PID: the
// NVIDIA driver reports host process identifiers even inside a container.
type GPUProcess struct {
	PID           string
	UsedMemoryMiB int
}

// ReadGPUProcesses lists every compute process on the GPU the running container
// is pinned to, including processes of other containers. Like ReadGPUTelemetry
// it runs one fixed read-only command and never starts a container.
func (sdk *DockerSDK) ReadGPUProcesses(ctx context.Context, id, uuid string) ([]GPUProcess, error) {
	output, err := sdk.runNvidiaSMI(ctx, id, uuid, "--query-compute-apps=pid,gpu_uuid,used_memory")
	if err != nil {
		return nil, err
	}
	return parseGPUProcesses(output, uuid)
}

func (sdk *DockerSDK) runNvidiaSMI(ctx context.Context, id, uuid, query string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := sdk.GetContainerInfo(ctx, id)
	if err != nil || info.Lifecycle != "running" || info.GPUUUID != uuid || !gpuUUIDPattern.MatchString(uuid) {
		return "", errors.New("GPU telemetry probe is unavailable")
	}
	command := []string{"/usr/bin/nvidia-smi", query, "--format=csv,noheader,nounits"}
	created, err := sdk.Client.ContainerExecCreate(ctx, info.ID, container.ExecOptions{Cmd: command, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return "", errors.New("GPU telemetry probe is unavailable")
	}
	attached, err := sdk.Client.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", errors.New("GPU telemetry probe is unavailable")
	}
	defer attached.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			attached.Close()
		case <-finished:
		}
	}()
	output, stderr := &boundedTelemetryWriter{}, &boundedTelemetryWriter{}
	if _, err := stdcopy.StdCopy(output, stderr, attached.Reader); err != nil {
		return "", errors.New("GPU telemetry read failed")
	}
	state, err := sdk.Client.ContainerExecInspect(ctx, created.ID)
	if err != nil || state.Running || state.ExitCode != 0 {
		return "", errors.New("GPU telemetry command failed")
	}
	return output.String(), nil
}

// parseGPUProcesses keeps the rows of the expected physical GPU. A process
// whose memory the driver cannot report ("[N/A]") is listed with zero MiB so it
// is still attributed; any other malformed row rejects the whole sample.
func parseGPUProcesses(value, uuid string) ([]GPUProcess, error) {
	reader := csv.NewReader(strings.NewReader(value))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = 3
	processes := []GPUProcess{}
	for {
		row, err := reader.Read()
		if err == io.EOF {
			return processes, nil
		}
		if err != nil {
			return nil, errors.New("invalid GPU process telemetry")
		}
		pid, err := strconv.Atoi(strings.TrimSpace(row[0]))
		if err != nil || pid <= 0 {
			return nil, errors.New("invalid GPU process telemetry")
		}
		if strings.TrimSpace(row[1]) != uuid {
			continue
		}
		memory := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(row[2]), "MiB"))
		used := 0
		if !strings.Contains(memory, "N/A") {
			if used, err = strconv.Atoi(memory); err != nil || used < 0 {
				return nil, errors.New("invalid GPU process telemetry")
			}
		}
		processes = append(processes, GPUProcess{PID: strconv.Itoa(pid), UsedMemoryMiB: used})
	}
}

func parseGPUTelemetry(value, uuid string, at time.Time) (GPUTelemetry, error) {
	reader := csv.NewReader(strings.NewReader(value))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = 7
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return GPUTelemetry{}, errors.New("invalid GPU telemetry")
		}
		if strings.TrimSpace(row[0]) != uuid {
			continue
		}
		numbers := make([]int, 5)
		for i, column := range []int{1, 3, 4, 5, 6} {
			number, err := strconv.Atoi(strings.TrimSpace(row[column]))
			if err != nil || number < 0 {
				return GPUTelemetry{}, errors.New("invalid GPU telemetry")
			}
			numbers[i] = number
		}
		if numbers[1] == 0 || numbers[2] > numbers[1] || numbers[3] > numbers[1] || numbers[4] > 100 {
			return GPUTelemetry{}, errors.New("invalid GPU telemetry")
		}
		return GPUTelemetry{UUID: uuid, Index: numbers[0], Name: strings.TrimSpace(row[2]), TotalMemoryMiB: numbers[1], UsedMemoryMiB: numbers[2], FreeMemoryMiB: numbers[3], UtilizationPercent: numbers[4], SampledAt: at}, nil
	}
	return GPUTelemetry{}, errors.New("expected GPU was not returned by telemetry probe")
}
