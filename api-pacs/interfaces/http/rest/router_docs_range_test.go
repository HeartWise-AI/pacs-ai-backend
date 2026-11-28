package rest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
)

const (
	docsRangeTestPassword = "placeholder-docs-password"
	docsRangeTestUser     = "sudo"
	// Upstream Go 1.26.9 ignores Range headers with more than 200 ranges.
	docsRangeLimit = 200
)

func TestDocsFileServerDoesNotExpandExcessiveRangeHeaders(t *testing.T) {
	body := []byte(strings.Repeat("specimen-docs-body-", 128))
	require.Greater(t, len(body), docsRangeLimit)

	docsRoot := t.TempDir()
	orthancRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(docsRoot, "specimen.bin"), body, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(orthancRoot, "specimen.bin"), body, 0o644))

	t.Setenv("API_NAME", "api-pacs")
	t.Setenv("OPENAPI_DOCS_PASSWORD", docsRangeTestPassword)

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.BasicAuth(os.Getenv("API_NAME"), map[string]string{
			"sudo": os.Getenv("OPENAPI_DOCS_PASSWORD"),
		}))
		FileServer(r, "/docs", http.Dir(docsRoot))
		FileServer(r, "/docs/orthanc", http.Dir(orthancRoot))
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := server.Client()

	for _, path := range []string{"/docs/specimen.bin", "/docs/orthanc/specimen.bin"} {
		t.Run(path, func(t *testing.T) {
			anonymous := doDocs(t, client, getDocs(t, client, server.URL+path, "", ""))
			require.Equal(t, http.StatusUnauthorized, anonymous.StatusCode)
			require.NotContains(t, string(anonymous.Body), "specimen-docs-body-")

			single := getDocs(t, client, server.URL+path, docsRangeTestUser, docsRangeTestPassword)
			single.Header.Set("Range", "bytes=0-0")
			singleResponse := doDocs(t, client, single)
			require.Equal(t, http.StatusPartialContent, singleResponse.StatusCode)
			require.Equal(t, body[:1], singleResponse.Body)

			started := time.Now()
			excessive := getDocs(t, client, server.URL+path, docsRangeTestUser, docsRangeTestPassword)
			excessive.Header.Set("Range", manySingleByteRanges(len(body)))
			excessiveResponse := doDocs(t, client, excessive)
			elapsed := time.Since(started)

			parts := strings.Count(string(excessiveResponse.Body), "Content-Range:")
			require.LessOrEqual(t, parts, docsRangeLimit, "elapsed %s status %d", elapsed, excessiveResponse.StatusCode)
			switch excessiveResponse.StatusCode {
			case http.StatusOK:
				require.Equal(t, body, excessiveResponse.Body)
			case http.StatusBadRequest, http.StatusRequestedRangeNotSatisfiable, http.StatusRequestHeaderFieldsTooLarge:
			default:
				t.Fatalf("status %d after %s with %d content ranges", excessiveResponse.StatusCode, elapsed, parts)
			}
		})
	}
}

func manySingleByteRanges(n int) string {
	var b strings.Builder
	b.Grow(len("bytes=") + n*4)
	b.WriteString("bytes=")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("0-0")
	}
	return b.String()
}

type docsResponse struct {
	StatusCode int
	Body       []byte
}

func getDocs(t *testing.T, client *http.Client, url, user, password string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if user != "" || password != "" {
		request.SetBasicAuth(user, password)
	}
	return request
}

func doDocs(t *testing.T, client *http.Client, request *http.Request) docsResponse {
	t.Helper()
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return docsResponse{StatusCode: response.StatusCode, Body: payload}
}
