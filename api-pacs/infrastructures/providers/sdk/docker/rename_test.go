package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

func renameTestSDK(t *testing.T, handler http.HandlerFunc) *DockerSDK {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &DockerSDK{Client: c}
}

func TestRenameContainerAsksTheEngineForTheExactName(t *testing.T) {
	const containerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	calls := 0
	sdk := renameTestSDK(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1.47/containers/"+containerID+"/rename" {
			t.Errorf("unexpected engine call %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "DeepRV-CLIP-pre-move-aaaaaaaaaaaa" {
			t.Errorf("renamed to %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := sdk.RenameContainer(context.Background(), containerID, "/DeepRV-CLIP-pre-move-aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("engine calls=%d", calls)
	}
}

func TestRenameContainerReportsANameAnotherContainerHolds(t *testing.T) {
	sdk := renameTestSDK(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"message": "name is already in use"})
	})
	err := sdk.RenameContainer(context.Background(), "candidate", "DeepRV-CLIP")
	if !errdefs.IsConflict(err) {
		t.Fatalf("expected a conflict, got %v", err)
	}
}

func TestRenameContainerRefusesAnUnusableNameWithoutCallingTheEngine(t *testing.T) {
	sdk := renameTestSDK(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the engine was called")
	})
	for _, name := range []string{"", " ", "/", "a", "-leading-dash", "has space", "has/slash", strings.Repeat("é", 4)} {
		if err := sdk.RenameContainer(context.Background(), "candidate", name); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
	if err := sdk.RenameContainer(context.Background(), " ", "valid-name"); err == nil {
		t.Error("an empty container reference was accepted")
	}
}

func TestStopContainerAcceptsAContainerThatIsAlreadyStopped(t *testing.T) {
	sdk := renameTestSDK(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/containers/old/stop") {
			t.Errorf("unexpected engine call %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotModified)
	})
	if err := sdk.StopContainer(context.Background(), "old"); err != nil {
		t.Fatalf("stopping a stopped container returned %v", err)
	}
}
