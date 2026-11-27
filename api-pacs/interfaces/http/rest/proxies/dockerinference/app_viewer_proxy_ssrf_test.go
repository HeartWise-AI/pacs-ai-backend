package dockerinference

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"

	"api-pacs/interfaces/http/rest/middlewares/cors"
	"api-pacs/interfaces/http/rest/middlewares/peeraddr"
)

const viewerProxySentinel = "viewer-proxy-ssrf-sentinel"

var errDialBlocked = errors.New("dial blocked by test")

// appViewerProxyRouter mounts AppViewerProxy the way InitRouter does: the
// docker-inference viewer route sits on /proxy with the process-wide
// middleware and without a session guard. The orthanc proxy beside it is
// the route that does use TokenSessionOrthancProxyAuthGuard.
func appViewerProxyRouter() http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(peeraddr.Capture)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(cors.Init().Handler)
	router.Use(middleware.Recoverer)
	router.Route("/proxy", func(r chi.Router) {
		r.Handle("/docker-inference/{containerName}/app/viewer/*", (&DockerInferenceProxy{}).AppViewerProxy())
	})
	return router
}

type recordedUpstream struct {
	mu       sync.Mutex
	hits     int
	method   string
	path     string
	rawQuery string
	body     string
	host     string
}

func (u *recordedUpstream) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.hits++
		u.method = r.Method
		u.path = r.URL.Path
		u.rawQuery = r.URL.RawQuery
		u.body = string(body)
		u.host = r.Host
		u.mu.Unlock()
		w.Header().Set("X-Upstream-Sentinel", viewerProxySentinel)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, viewerProxySentinel)
	})
}

func (u *recordedUpstream) snapshot() recordedUpstream {
	u.mu.Lock()
	defer u.mu.Unlock()
	return recordedUpstream{
		hits: u.hits, method: u.method, path: u.path, rawQuery: u.rawQuery, body: u.body, host: u.host,
	}
}

func viewerProxyRequest(apiBase, containerName, viewerPath, rawQuery, method, body string) (*http.Request, error) {
	endpoint, err := url.Parse(apiBase)
	if err != nil {
		return nil, err
	}
	endpoint.Path = "/proxy/docker-inference/" + containerName + "/app/viewer/" + viewerPath
	endpoint.RawQuery = rawQuery
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, endpoint.String(), reader)
	if err != nil {
		return nil, err
	}
	return request, nil
}

func TestAppViewerProxyDoesNotReachAttackerChosenHost(t *testing.T) {
	upstreamState := &recordedUpstream{}
	upstream := httptest.NewServer(upstreamState.handler())
	t.Cleanup(upstream.Close)
	api := httptest.NewServer(appViewerProxyRouter())
	t.Cleanup(api.Close)

	hostPort := upstream.Listener.Addr().String()
	// Own transport so this test does not share DefaultTransport with the dial hook below.
	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	cases := []struct {
		name       string
		method     string
		viewerPath string
		rawQuery   string
		body       string
		bearer     string
	}{
		{name: "get", method: http.MethodGet, viewerPath: "index.html", rawQuery: "scan=1"},
		{name: "get nested", method: http.MethodGet, viewerPath: "assets/app.js"},
		{name: "post", method: http.MethodPost, viewerPath: "submit", body: "post-body-marker"},
		{name: "put", method: http.MethodPut, viewerPath: "resource", body: "put-body-marker"},
		{name: "patch", method: http.MethodPatch, viewerPath: "resource", body: "patch-body-marker"},
		{name: "delete", method: http.MethodDelete, viewerPath: "resource"},
		{name: "head", method: http.MethodHead, viewerPath: "index.html"},
		{name: "options", method: http.MethodOptions, viewerPath: "index.html"},
		{name: "query", method: "QUERY", viewerPath: "index.html", body: "query-body-marker"},
		{name: "trace", method: http.MethodTrace, viewerPath: "index.html"},
		{name: "bearer is not a session", method: http.MethodGet, viewerPath: "index.html", bearer: "Bearer not-a-session"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := upstreamState.snapshot()
			request, err := viewerProxyRequest(api.URL, hostPort, tc.viewerPath, tc.rawQuery, tc.method, tc.body)
			require.NoError(t, err)
			if tc.bearer != "" {
				request.Header.Set("Authorization", tc.bearer)
			}
			response, err := client.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			responseBody, err := io.ReadAll(response.Body)
			require.NoError(t, err)

			after := upstreamState.snapshot()
			require.Equal(t, before.hits, after.hits, "upstream received %s %s?%s body %q host %q", after.method, after.path, after.rawQuery, after.body, after.host)
			require.NotContains(t, string(responseBody), viewerProxySentinel)
			require.Empty(t, response.Header.Get("X-Upstream-Sentinel"))
			require.GreaterOrEqual(t, response.StatusCode, 400)
			require.Less(t, response.StatusCode, 500)
		})
	}
}

func TestAppViewerProxyDoesNotDialArbitraryHosts(t *testing.T) {
	base, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	var mu sync.Mutex
	var dials []string
	hooked := base.Clone()
	hooked.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dials = append(dials, network+" "+addr)
		mu.Unlock()
		return nil, errDialBlocked
	}
	http.DefaultTransport = hooked
	t.Cleanup(func() { http.DefaultTransport = base })

	router := appViewerProxyRouter()

	// Names an unauthenticated caller can put in {containerName}. None of
	// these is a registered model container for this process.
	targets := []string{
		"study-service",
		"study-service:8600",
		"model-container",
		"attacker.test",
		"attacker.test:443",
		"10.1.2.3",
		"10.1.2.3:8600",
		"[::1]:9",
	}
	for _, containerName := range targets {
		t.Run(containerName, func(t *testing.T) {
			mu.Lock()
			dials = nil
			mu.Unlock()

			request := httptest.NewRequest(http.MethodPost, "http://api.invalid/proxy/docker-inference/placeholder/app/viewer/index.html", strings.NewReader("relay-body"))
			request.URL.Path = "/proxy/docker-inference/" + containerName + "/app/viewer/index.html"
			request.URL.RawPath = ""
			request.URL.RawQuery = "scan=1"
			request.RequestURI = request.URL.RequestURI()
			request.Header.Set("Authorization", "Bearer not-a-session")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			mu.Lock()
			seen := append([]string(nil), dials...)
			mu.Unlock()
			require.Empty(t, seen, "proxy dialed %v and returned %d %q", seen, recorder.Code, recorder.Body.String())
			require.NotContains(t, recorder.Body.String(), viewerProxySentinel)
			require.GreaterOrEqual(t, recorder.Code, 400)
			require.Less(t, recorder.Code, 500)
		})
	}
}
