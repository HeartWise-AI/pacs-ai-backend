package modelmanager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	dockererrdefs "github.com/docker/docker/errdefs"

	"api-pacs/infrastructures/providers/api/dockerinference"
	api "api-pacs/infrastructures/providers/api/dockerinference/types"
	docker "api-pacs/infrastructures/providers/sdk/docker/types"
)

type dockerFake struct {
	mu         sync.Mutex
	name       string
	running    bool
	starts     int
	startRace  bool
	pid        string
	inspectErr error
}

func (d *dockerFake) GetContainerInfo(context.Context, string) (docker.GetContainerInfoResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inspectErr != nil {
		return docker.GetContainerInfoResult{}, d.inspectErr
	}
	return docker.GetContainerInfoResult{Name: "/" + d.name, Running: d.running}, nil
}
func (d *dockerFake) StartContainer(context.Context, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.running = true
	d.starts++
	if d.startRace {
		return errors.New("already started")
	}
	return nil
}
func (d *dockerFake) InferenceProcessIDs(context.Context, string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return []string{d.pid}, nil
}

func TestContainerBackendStartRaceAndDeferredProcessExit(t *testing.T) {
	d := &dockerFake{pid: "10", startRace: true}
	var mu sync.Mutex
	loaded := false
	unloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/inference/model-info" {
			json.NewEncoder(w).Encode(api.GetModelInfoResponse{Success: true, Data: api.ModelInfo{Resources: resources(30, 80)}})
			return
		}
		switch r.URL.Path {
		case "/api/inference/model/load":
			if r.Method != "POST" {
				t.Error("load method")
			}
			loaded = true
		case "/api/inference/model/unload":
			if r.Method != "POST" {
				t.Error("unload method")
			}
			loaded = false
			unloads++
		case "/api/inference/model/runtime":
			if r.Method != "GET" {
				t.Error("runtime method")
			}
		default:
			http.NotFound(w, r)
			return
		}
		state := api.RuntimeUnloaded
		if loaded {
			state = api.RuntimeReady
		}
		json.NewEncoder(w).Encode(api.ModelRuntimeResponse{Success: true, Data: api.ModelRuntime{State: state, Loaded: loaded}})
	}))
	defer server.Close()
	d.name = strings.TrimPrefix(server.URL, "http://")
	b := &ContainerBackend{Docker: d, API: &dockerinference.DockerInferenceAPI{}, PollInterval: time.Millisecond, ReadinessTimeout: time.Second}
	model, err := b.Prepare(context.Background(), id(1))
	if err != nil {
		t.Fatal(err)
	}
	if d.starts != 1 || model.Resources.PeakMemoryMiB != 80 {
		t.Fatal("start/readiness not shared")
	}
	if err = b.Load(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err = b.Unload(ctx, model); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acknowledgement released reservation before process exit: %v", err)
	}
	// Retry must remember the pending old PID even though runtime says UNLOADED.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if err = b.Unload(ctx2, model); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry forgot pending process: %v", err)
	}
	d.mu.Lock()
	d.pid = "11"
	d.mu.Unlock()
	if err = b.Unload(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if unloads != 1 {
		t.Fatalf("unexpected repeated unload: %d", unloads)
	}
}

func TestBackendMalformedRuntimeCannotReleaseReservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"state":"UNLOADED"}}`))
	}))
	defer server.Close()
	d := &dockerFake{name: strings.TrimPrefix(server.URL, "http://"), running: true, pid: "10"}
	b := &ContainerBackend{Docker: d, API: &dockerinference.DockerInferenceAPI{}}
	if err := b.Unload(context.Background(), Model{Name: d.name, ContainerID: id(1)}); err == nil {
		t.Fatal("malformed runtime accepted")
	}
}

func TestBackendInspectDoesNotStartStoppedContainer(t *testing.T) {
	d := &dockerFake{name: "stopped-model", running: false}
	b := &ContainerBackend{Docker: d, API: &dockerinference.DockerInferenceAPI{}}
	model, runtime, err := b.Inspect(context.Background(), id(1))
	if err != nil {
		t.Fatal(err)
	}
	if d.starts != 0 || model.Name != "stopped-model" || runtime.State != api.RuntimeUnloaded {
		t.Fatalf("stopped inspection changed container state: model=%+v runtime=%+v starts=%d", model, runtime, d.starts)
	}
}

func TestBackendInspectClassifiesMissingContainer(t *testing.T) {
	d := &dockerFake{inspectErr: dockererrdefs.NotFound(errors.New("no such container"))}
	b := &ContainerBackend{Docker: d, API: &dockerinference.DockerInferenceAPI{}}
	model, _, err := b.Inspect(context.Background(), id(1))
	if !errors.Is(err, ErrContainerMissing) || model.ContainerID != id(1) {
		t.Fatalf("missing container was not classified: model=%+v err=%v", model, err)
	}
}

func TestBackendInspectReadsRunningRuntimeWithoutLoading(t *testing.T) {
	d := &dockerFake{name: "running-model", running: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/inference/model-info":
			json.NewEncoder(w).Encode(api.GetModelInfoResponse{
				Success: true, Data: api.ModelInfo{Resources: resources(30, 80)},
			})
		case "/api/inference/model/runtime":
			json.NewEncoder(w).Encode(api.ModelRuntimeResponse{
				Success: true, Data: api.ModelRuntime{State: api.RuntimeReady, Loaded: true},
			})
		default:
			t.Fatal("unexpected lifecycle request: " + r.URL.Path)
		}
	}))
	defer server.Close()
	d.name = strings.TrimPrefix(server.URL, "http://")
	b := &ContainerBackend{Docker: d, API: &dockerinference.DockerInferenceAPI{}}
	model, runtime, err := b.Inspect(context.Background(), id(1))
	if err != nil {
		t.Fatal(err)
	}
	if d.starts != 0 || model.Resources.PeakMemoryMiB != 80 || runtime.State != api.RuntimeReady {
		t.Fatalf("running inspection mutated or misread state: model=%+v runtime=%+v starts=%d", model, runtime, d.starts)
	}
}
