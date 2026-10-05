package modelmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"api-pacs/infrastructures/providers/api/dockerinference"
	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func TestManagerReconcilesStoppedContainerThenColdPredictsThroughLifecycleAPI(t *testing.T) {
	var mu sync.Mutex
	loaded := false
	loads, predictions := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/api/inference/model-info":
			json.NewEncoder(w).Encode(types.GetModelInfoResponse{
				Success: true,
				Data:    types.ModelInfo{Resources: resources(30, 80)},
			})
		case "/api/inference/model/runtime":
			state := types.RuntimeUnloaded
			if loaded {
				state = types.RuntimeReady
			}
			json.NewEncoder(w).Encode(types.ModelRuntimeResponse{
				Success: true,
				Data:    types.ModelRuntime{State: state, Loaded: loaded},
			})
		case "/api/inference/model/load":
			if r.Method != http.MethodPost {
				t.Fatalf("load used %s", r.Method)
			}
			loaded = true
			loads++
			json.NewEncoder(w).Encode(types.ModelRuntimeResponse{
				Success: true,
				Data:    types.ModelRuntime{State: types.RuntimeReady, Loaded: true},
			})
		case "/api/inference/predict":
			if r.Method != http.MethodPost || !loaded {
				t.Fatalf("prediction reached an unloaded model with method %s", r.Method)
			}
			predictions++
			json.NewEncoder(w).Encode(types.PredictResponse{Success: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	docker := &dockerFake{
		name:    strings.TrimPrefix(server.URL, "http://"),
		running: false,
		pid:     "10",
	}
	backend := &ContainerBackend{
		Docker: docker, API: &dockerinference.DockerInferenceAPI{},
		ReadinessTimeout: time.Second, PollInterval: time.Millisecond,
	}
	manager, err := New(Config{
		Enabled: true, RequireReconciliation: true, BudgetMiB: 100,
		QueueTimeout: time.Second, OperationTimeout: time.Second,
	}, backend)
	if err != nil {
		t.Fatal(err)
	}

	if err = manager.Reconcile(context.Background(), []string{id(1)}); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if !snapshot.Ready || snapshot.ReservedMiB != 0 || snapshot.Models[id(1)].State != types.RuntimeUnloaded {
		t.Fatalf("stopped container was not reconciled as unloaded: %+v", snapshot)
	}
	if docker.starts != 0 {
		t.Fatal("reconciliation started the stopped container")
	}

	response, err := manager.Predict(context.Background(), id(1), types.PredictRequest{})
	if err != nil || !response.Success {
		t.Fatalf("cold managed prediction failed: response=%+v err=%v", response, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if docker.starts != 1 || loads != 1 || predictions != 1 {
		t.Fatalf("unexpected lifecycle calls: starts=%d loads=%d predictions=%d", docker.starts, loads, predictions)
	}
	snapshot = manager.Snapshot()
	if snapshot.ReservedMiB != 30 || snapshot.Models[id(1)].State != types.RuntimeReady || !snapshot.Models[id(1)].Loaded {
		t.Fatalf("successful prediction did not retain the resident model: %+v", snapshot)
	}
}
