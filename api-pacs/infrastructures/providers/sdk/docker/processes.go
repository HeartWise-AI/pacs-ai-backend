package docker

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// InferenceProcessIDs returns host PIDs for the single supervised API worker.
// Tracking the old PID prevents treating an unload acknowledgement as VRAM release.
func (d *DockerSDK) InferenceProcessIDs(ctx context.Context, id string) ([]string, error) {
	return d.processIDs(ctx, id, func(args string) bool {
		uvicorn, app := false, false
		for _, arg := range strings.Fields(args) {
			if filepath.Base(arg) == "uvicorn" {
				uvicorn = true
			}
			if arg == "main:app" {
				app = true
			}
		}
		return uvicorn && app
	})
}

// ContainerProcessIDs returns the host PID of every process in the container,
// so GPU memory held by a worker or subprocess is attributed to its model.
func (d *DockerSDK) ContainerProcessIDs(ctx context.Context, id string) ([]string, error) {
	return d.processIDs(ctx, id, func(string) bool { return true })
}

func (d *DockerSDK) processIDs(ctx context.Context, id string, match func(string) bool) ([]string, error) {
	top, err := d.Client.ContainerTop(ctx, id, []string{"-eo", "pid,args"})
	if err != nil {
		return nil, err
	}
	pidColumn, argsColumn := -1, -1
	for i, title := range top.Titles {
		switch strings.ToUpper(title) {
		case "PID":
			pidColumn = i
		case "COMMAND", "CMD", "ARGS":
			argsColumn = i
		}
	}
	if pidColumn < 0 || argsColumn < 0 {
		return nil, fmt.Errorf("Docker process list lacks PID/command columns")
	}
	var pids []string
	for _, row := range top.Processes {
		if pidColumn >= len(row) || argsColumn >= len(row) {
			return nil, fmt.Errorf("malformed Docker process row")
		}
		if match(row[argsColumn]) {
			pids = append(pids, row[pidColumn])
		}
	}
	return pids, nil
}
