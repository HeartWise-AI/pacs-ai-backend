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
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := sdk.GetContainerInfo(ctx, id)
	if err != nil || info.Lifecycle != "running" || info.GPUUUID != uuid || !gpuUUIDPattern.MatchString(uuid) {
		return GPUTelemetry{}, errors.New("GPU telemetry probe is unavailable")
	}
	command := []string{"/usr/bin/nvidia-smi", "--query-gpu=uuid,index,name,memory.total,memory.used,memory.free,utilization.gpu", "--format=csv,noheader,nounits"}
	created, err := sdk.Client.ContainerExecCreate(ctx, info.ID, container.ExecOptions{Cmd: command, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return GPUTelemetry{}, errors.New("GPU telemetry probe is unavailable")
	}
	attached, err := sdk.Client.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return GPUTelemetry{}, errors.New("GPU telemetry probe is unavailable")
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
		return GPUTelemetry{}, errors.New("GPU telemetry read failed")
	}
	state, err := sdk.Client.ContainerExecInspect(ctx, created.ID)
	if err != nil || state.Running || state.ExitCode != 0 {
		return GPUTelemetry{}, errors.New("GPU telemetry command failed")
	}
	return parseGPUTelemetry(output.String(), uuid, time.Now().UTC())
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
