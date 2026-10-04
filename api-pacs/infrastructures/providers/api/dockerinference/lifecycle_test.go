package dockerinference

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLifecycleClientRejectsFailedAndInvalidResponses(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"HTTP failure", `{}`, 500},
		{"logical failure", `{"success":false,"data":{"state":"ERROR","loaded":false,"activeRequests":0,"lastUsedAt":null}}`, 200},
		{"missing state", `{"success":true,"data":{}}`, 200},
		{"unknown state", `{"success":true,"data":{"state":"WHAT","loaded":false,"activeRequests":0,"lastUsedAt":null}}`, 200},
		{"inconsistent", `{"success":true,"data":{"state":"UNLOADED","loaded":true,"activeRequests":0,"lastUsedAt":null}}`, 200},
		{"negative active", `{"success":true,"data":{"state":"ERROR","loaded":false,"activeRequests":-1,"lastUsedAt":null}}`, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); w.Write([]byte(tt.body)) }))
			defer s.Close()
			_, err := (&DockerInferenceAPI{}).GetModelRuntime(context.Background(), strings.TrimPrefix(s.URL, "http://"))
			if err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestLifecycleContextAndRedirects(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := (&DockerInferenceAPI{}).LoadModel(ctx, strings.TrimPrefix(s.URL, "http://"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context lost: %v", err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://forbidden.invalid", 307) }))
	defer redirect.Close()
	_, err = (&DockerInferenceAPI{}).LoadModel(context.Background(), strings.TrimPrefix(redirect.URL, "http://"))
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect followed: %v", err)
	}
	_, err = (&DockerInferenceAPI{}).LoadModel(context.Background(), "host/path")
	if err == nil {
		t.Fatal("arbitrary path accepted")
	}
}
