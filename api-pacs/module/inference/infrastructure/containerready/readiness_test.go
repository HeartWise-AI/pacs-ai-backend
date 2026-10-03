package containerready

import (
	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"context"
	"errors"
	"testing"
	"time"
)

type delayedDocker struct{ delay time.Duration }

func (d delayedDocker) GetContainerInfo(context.Context, string) (docker.GetContainerInfoResult, error) {
	return docker.GetContainerInfoResult{Name: "/test-model", Running: false}, nil
}
func (d delayedDocker) StartContainer(ctx context.Context, _ string) error {
	timer := time.NewTimer(d.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type readyMetadata struct{}

func (readyMetadata) GetModelInfo(ctx context.Context, _ string) (api.GetModelInfoResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.GetModelInfoResponse{}, err
	}
	return api.GetModelInfoResponse{Success: true}, nil
}
func TestReadinessWindowStartsAfterDockerStartup(t *testing.T) {
	name, _, err := Ensure(context.Background(), delayedDocker{20 * time.Millisecond}, readyMetadata{}, "id", 5*time.Millisecond, time.Millisecond)
	if err != nil || name != "test-model" {
		t.Fatalf("startup consumed readiness window: name=%s err=%v", name, err)
	}
}
func TestCallerDeadlineStillBoundsDockerStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, _, err := Ensure(ctx, delayedDocker{time.Second}, readyMetadata{}, "id", time.Second, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller deadline was lost: %v", err)
	}
}
