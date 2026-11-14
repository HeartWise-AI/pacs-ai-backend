package dockerinference

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

func TestRenderReportPostsOnlyStoredResultWithoutCredentials(t *testing.T) {
	var gotPath, gotAuth, gotCookie string
	var gotBody map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		payload, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(payload, &gotBody)
		_, _ = w.Write([]byte(`{"success":true,"data":{"htmlBase64":"PGgxPg==","modelVersion":"1.3.0"}}`))
	}))
	defer server.Close()

	response, err := (&DockerInferenceAPI{}).RenderReport(context.Background(), strings.TrimPrefix(server.URL, "http://"),
		types.RenderReportRequest{Result: json.RawMessage(`{"predictions":{"x":1}}`)}, 1024)

	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/inference/render-report" || gotAuth != "" || gotCookie != "" {
		t.Fatalf("unexpected request path=%q auth=%q cookie=%q", gotPath, gotAuth, gotCookie)
	}
	if string(gotBody["result"]) != `{"predictions":{"x":1}}` || len(gotBody) != 1 {
		t.Fatalf("unexpected body %v", gotBody)
	}
	if response.Data.HTMLBase64 != "PGgxPg==" || response.Data.ModelVersion != "1.3.0" {
		t.Fatalf("unexpected response %+v", response)
	}
}

func TestRenderReportMapsStatusesAndEnforcesSizeCap(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		limit  int64
		want   error
	}{
		{"unusable result", http.StatusUnprocessableEntity, `{}`, 1024, types.ErrRenderReportRejected},
		{"endpoint missing", http.StatusNotFound, `{}`, 1024, types.ErrRenderReportUnsupported},
		{"logical failure", http.StatusOK, `{"success":false}`, 1024, types.ErrRenderReportRejected},
		{"too large", http.StatusOK, `{"success":true,"data":{"htmlBase64":"` + strings.Repeat("A", 200) + `"}}`, 64, types.ErrRenderReportTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			_, err := (&DockerInferenceAPI{}).RenderReport(context.Background(), strings.TrimPrefix(server.URL, "http://"),
				types.RenderReportRequest{Result: json.RawMessage(`{}`)}, tt.limit)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRenderReportRejectsInvalidHostAndHonoursDeadline(t *testing.T) {
	if _, err := (&DockerInferenceAPI{}).RenderReport(context.Background(), "evil.example/path", types.RenderReportRequest{}, 1); err == nil {
		t.Fatal("invalid host accepted")
	}
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-blocked }))
	defer server.Close()
	defer close(blocked)
	ctx, cancel := context.WithTimeout(context.Background(), 20_000_000)
	defer cancel()
	if _, err := (&DockerInferenceAPI{}).RenderReport(ctx, strings.TrimPrefix(server.URL, "http://"), types.RenderReportRequest{Result: json.RawMessage(`{}`)}, 1024); err == nil {
		t.Fatal("deadline not enforced")
	}
}
