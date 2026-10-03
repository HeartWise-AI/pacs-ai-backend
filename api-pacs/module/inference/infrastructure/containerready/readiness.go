// Package containerready shares Docker startup and HTTP readiness between
// ingestion dispatch and the model manager.
package containerready

import (
	"context"
	"fmt"
	"strings"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
)

type Docker interface {
	GetContainerInfo(context.Context, string) (docker.GetContainerInfoResult, error)
	StartContainer(context.Context, string) error
}
type Metadata interface {
	GetModelInfo(context.Context, string) (api.GetModelInfoResponse, error)
}

func Ensure(ctx context.Context, d Docker, a Metadata, id string, timeout, poll time.Duration) (string, api.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	info, err := d.GetContainerInfo(ctx, id)
	if err != nil {
		return "", api.ModelInfo{}, fmt.Errorf("cannot inspect inference container %s: %w", id, err)
	}
	if !info.Running {
		if err = d.StartContainer(ctx, id); err != nil {
			refreshed, inspectErr := d.GetContainerInfo(ctx, id)
			if inspectErr != nil || !refreshed.Running {
				return "", api.ModelInfo{}, fmt.Errorf("cannot start inference container %s: %w", id, err)
			}
			info = refreshed
		}
	}
	name := strings.TrimPrefix(strings.TrimSpace(info.Name), "/")
	if name == "" {
		return "", api.ModelInfo{}, fmt.Errorf("inference container %s has no resolvable name", id)
	}
	for {
		response, err := a.GetModelInfo(ctx, name)
		if err == nil && response.Success {
			return name, response.Data, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", api.ModelInfo{}, fmt.Errorf("inference container %s did not become ready: %w", id, ctx.Err())
		case <-timer.C:
		}
	}
}
