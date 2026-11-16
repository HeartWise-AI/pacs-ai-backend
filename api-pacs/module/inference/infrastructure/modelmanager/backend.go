package modelmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	dockererrdefs "github.com/docker/docker/errdefs"

	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/infrastructure/containerready"
)

var ErrContainerMissing = errors.New("registered model container does not exist")

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

// Inspect reports current state without starting a stopped container. A
// stopped container has no resident GPU allocation, so its resource contract
// can be loaded later by Prepare when the first prediction arrives.
func (b *ContainerBackend) Inspect(ctx context.Context, id string) (Model, api.ModelRuntime, error) {
	info, err := b.Docker.GetContainerInfo(ctx, id)
	if err != nil {
		if dockererrdefs.IsNotFound(err) {
			return Model{ContainerID: id}, api.ModelRuntime{}, fmt.Errorf("%w: %v", ErrContainerMissing, err)
		}
		return Model{ContainerID: id}, api.ModelRuntime{}, err
	}
	model := Model{ContainerID: id, Name: strings.TrimPrefix(info.Name, "/"), GPUUUID: info.GPUUUID, ImageID: info.ImageID}
	if info.Lifecycle != docker.LifecycleRunning {
		return model, api.ModelRuntime{State: api.RuntimeUnloaded}, nil
	}
	metadata, err := b.API.GetModelInfo(ctx, model.Name)
	if err != nil {
		return model, api.ModelRuntime{}, err
	}
	model.Resources = metadata.Data.Resources
	runtime, err := b.API.GetModelRuntime(ctx, model.Name)
	return model, runtime, err
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
	placement, err := b.Docker.GetContainerInfo(ctx, id)
	if err != nil {
		return Model{}, err
	}
	return Model{ContainerID: id, Name: name, Resources: info.Resources, GPUUUID: placement.GPUUUID, ImageID: placement.ImageID}, nil
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
	if r.Stream != nil {
		// A streamed request body is consumed as it is sent and cannot be sent
		// again. Confirm the inference process answers and holds the model
		// before the first byte leaves, waiting out a process respawn, so the
		// only failures left are those of a prediction already under way.
		runtime, err := b.runtimeDuringRestart(ctx, m)
		if err != nil {
			return api.PredictResponse{}, &ModelNotReadyError{cause: err}
		}
		if !runtime.Loaded || runtime.State != api.RuntimeReady {
			return api.PredictResponse{}, &ModelNotReadyError{cause: fmt.Errorf("model runtime is %s", runtime.State)}
		}
	}
	return b.API.Predict(ctx, m.Name, r)
}

// ModelNotReadyError reports that a streamed prediction was not started
// because the model did not confirm it was loaded. No request byte was sent.
type ModelNotReadyError struct{ cause error }

func (e *ModelNotReadyError) Error() string {
	return fmt.Sprintf("model is not ready for a streamed prediction: %v", e.cause)
}
func (e *ModelNotReadyError) Unwrap() error { return e.cause }

// ModelNotReady marks the error for the HTTP layer, which answers it as a
// transient upstream failure.
func (*ModelNotReadyError) ModelNotReady() bool { return true }

// runtimeDuringRestart reads the runtime, waiting out the short window in which
// the supervised inference process is being respawned. A model that has just
// exited (for example at its own idle timeout) answers 502 through its proxy
// for a second or two; failing the eviction on that answer would quarantine a
// reservation for memory that is in fact free.
func (b *ContainerBackend) runtimeDuringRestart(ctx context.Context, m Model) (api.ModelRuntime, error) {
	timeout, poll := b.timings()
	deadline := time.Now().Add(timeout)
	for {
		r, err := b.API.GetModelRuntime(ctx, m.Name)
		var status interface{ Restarting() bool }
		if err == nil || !errors.As(err, &status) || !status.Restarting() || !time.Now().Before(deadline) {
			return r, err
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return r, err
		case <-timer.C:
		}
	}
}

func (b *ContainerBackend) Unload(ctx context.Context, m Model) error {
	r, err := b.runtimeDuringRestart(ctx, m)
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
