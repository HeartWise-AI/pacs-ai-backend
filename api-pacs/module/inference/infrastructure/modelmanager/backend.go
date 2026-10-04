package modelmanager

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	"api-pacs/module/inference/infrastructure/containerready"
)

type Docker interface {
	containerready.Docker
	InferenceProcessIDs(context.Context, string) ([]string, error)
}
type LifecycleAPI interface {
	containerready.Metadata
	GetModelRuntime(context.Context, string) (api.ModelRuntime, error)
	LoadModel(context.Context, string) (api.ModelRuntime, error)
	UnloadModel(context.Context, string) (api.ModelRuntime, error)
	Predict(context.Context, string, api.PredictRequest) (api.PredictResponse, error)
}

type ContainerBackend struct {
	Docker           Docker
	API              LifecycleAPI
	ReadinessTimeout time.Duration
	PollInterval     time.Duration
	mu               sync.Mutex
	pending          map[string]string
}

func (b *ContainerBackend) timings() (time.Duration, time.Duration) {
	timeout, poll := b.ReadinessTimeout, b.PollInterval
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return timeout, poll
}
func (b *ContainerBackend) Prepare(ctx context.Context, id string) (Model, error) {
	timeout, poll := b.timings()
	name, info, err := containerready.Ensure(ctx, b.Docker, b.API, id, timeout, poll)
	if err != nil {
		return Model{}, err
	}
	if err = info.Resources.Validate(); err != nil {
		return Model{}, err
	}
	return Model{ContainerID: id, Name: name, Resources: info.Resources}, nil
}
func (b *ContainerBackend) Runtime(ctx context.Context, m Model) (api.ModelRuntime, error) {
	return b.API.GetModelRuntime(ctx, m.Name)
}
func (b *ContainerBackend) Load(ctx context.Context, m Model) error {
	r, err := b.API.LoadModel(ctx, m.Name)
	if err != nil {
		return err
	}
	if r.State != api.RuntimeReady || !r.Loaded || r.ActiveRequests != 0 {
		return fmt.Errorf("load did not produce an idle READY model")
	}
	return nil
}
func (b *ContainerBackend) Predict(ctx context.Context, m Model, r api.PredictRequest) (api.PredictResponse, error) {
	return b.API.Predict(ctx, m.Name, r)
}

func (b *ContainerBackend) Unload(ctx context.Context, m Model) error {
	r, err := b.API.GetModelRuntime(ctx, m.Name)
	if err != nil {
		return err
	}
	b.mu.Lock()
	oldPID := b.pending[m.ContainerID]
	b.mu.Unlock()
	if r.State == api.RuntimeUnloaded && !r.Loaded && r.ActiveRequests == 0 && oldPID == "" {
		return nil
	}
	if oldPID == "" {
		before, err := b.Docker.InferenceProcessIDs(ctx, m.ContainerID)
		if err != nil {
			return err
		}
		if len(before) != 1 {
			return fmt.Errorf("expected one supervised Uvicorn process before unload, got %d", len(before))
		}
		oldPID = before[0]
		b.mu.Lock()
		if b.pending == nil {
			b.pending = make(map[string]string)
		}
		b.pending[m.ContainerID] = oldPID
		b.mu.Unlock()
	}
	if r.State != api.RuntimeUnloaded {
		if _, err = b.API.UnloadModel(ctx, m.Name); err != nil {
			return err
		}
	}
	_, poll := b.timings()
	// Phase 2 acknowledges before SIGTERM. Keep the reservation until Docker
	// confirms that process has exited and its replacement reports UNLOADED.
	for {
		after, processErr := b.Docker.InferenceProcessIDs(ctx, m.ContainerID)
		if processErr == nil && len(after) == 1 && !slices.Contains(after, oldPID) {
			r, runtimeErr := b.API.GetModelRuntime(ctx, m.Name)
			if runtimeErr == nil && r.State == api.RuntimeUnloaded && !r.Loaded && r.ActiveRequests == 0 {
				b.mu.Lock()
				delete(b.pending, m.ContainerID)
				b.mu.Unlock()
				return nil
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("unload process restart was not confirmed: %w", ctx.Err())
		case <-timer.C:
		}
	}
}
