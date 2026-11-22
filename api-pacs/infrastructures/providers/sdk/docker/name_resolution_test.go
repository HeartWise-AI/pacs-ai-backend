package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
)

func TestGetContainerInfoResolvesOnlyUnambiguousCaseInsensitiveNames(t *testing.T) {
	const canonicalID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const otherID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name, reference, listing                           string
		originalStatus                                     int
		wantList, wantCanonical, wantSuccess, wantNotFound bool
	}{
		{name: "lowercased gateway hostname", reference: "deepcoro-cto", originalStatus: 404, listing: `[{"Id":"` + canonicalID + `","Names":["/DeepCORO-CTO"]}]`, wantList: true, wantCanonical: true, wantSuccess: true},
		{name: "duplicate aliases for same canonical identity", reference: "deeprv-clip", originalStatus: 404, listing: `[{"Id":"` + canonicalID + `","Names":["/DeepRV-CLIP","/DEEPRV-CLIP"]}]`, wantList: true, wantCanonical: true, wantSuccess: true},
		{name: "missing name preserves not found", reference: "missing", originalStatus: 404, listing: `[{"Id":"` + canonicalID + `","Names":["/unrelated"]}]`, wantList: true, wantNotFound: true},
		{name: "ambiguous case variants fail closed", reference: "deepcoro-cto", originalStatus: 404, listing: `[{"Id":"` + canonicalID + `","Names":["/DeepCORO-CTO"]},{"Id":"` + otherID + `","Names":["/DEEPCORO-CTO"]}]`, wantList: true},
		{name: "daemon error is not hidden", reference: "deepcoro-cto", originalStatus: 500},
		{name: "URL never triggers name discovery", reference: "http://deepcoro-cto:8000", originalStatus: 404, wantNotFound: true},
		{name: "exact name remains first choice", reference: "DeepCORO-CTO", originalStatus: 200, wantSuccess: true},
		{name: "exact canonical ID remains first choice", reference: canonicalID, originalStatus: 200, wantSuccess: true},
		{name: "missing canonical ID does not discover names", reference: canonicalID, originalStatus: 404, wantNotFound: true},
		{name: "missing short ID does not discover names", reference: canonicalID[:12], originalStatus: 404, wantNotFound: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listCalls, canonicalCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1.47/containers/json" {
					listCalls++
					if r.URL.Query().Get("all") != "1" {
						t.Error("discovery must include stopped containers")
					}
					w.Write([]byte(tc.listing))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/"+canonicalID+"/json") && tc.reference != canonicalID {
					canonicalCalls++
					writeNameResolutionInspect(w, canonicalID)
					return
				}
				if tc.originalStatus == 200 {
					writeNameResolutionInspect(w, canonicalID)
					return
				}
				w.WriteHeader(tc.originalStatus)
				json.NewEncoder(w).Encode(map[string]string{"message": "original inspect failed"})
			}))
			defer server.Close()
			c, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			result, err := (&DockerSDK{Client: c}).GetContainerInfo(context.Background(), tc.reference)
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if tc.wantNotFound && !errdefs.IsNotFound(err) {
				t.Fatalf("not-found identity lost: %v", err)
			}
			if tc.wantSuccess && result.ID != canonicalID {
				t.Fatalf("returned noncanonical identity: %+v", result)
			}
			if tc.wantSuccess && result.PlacementName != "DeepCORO-CTO" {
				t.Fatalf("stable placement name missing: %+v", result)
			}
			if (listCalls == 1) != tc.wantList || listCalls > 1 {
				t.Fatalf("list calls=%d", listCalls)
			}
			if (canonicalCalls == 1) != tc.wantCanonical || canonicalCalls > 1 {
				t.Fatalf("canonical inspect calls=%d", canonicalCalls)
			}
		})
	}
}

func writeNameResolutionInspect(w http.ResponseWriter, id string) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"Id": id, "Name": "/DeepCORO-CTO", "Image": "sha256:test",
		"Config": map[string]interface{}{"Labels": map[string]string{gpuPlacementNameLabel: "DeepCORO-CTO"}},
		"State":  map[string]interface{}{"Status": "running", "StartedAt": "2026-11-12T00:00:00Z", "FinishedAt": "0001-01-01T00:00:00Z"},
	})
}
