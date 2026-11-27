package dockerinference

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

const viewerPathPrefix = "/app/viewer/"

// ViewerResolver maps a viewer token to the model container it was issued for.
type ViewerResolver interface {
	ResolveWebAppViewer(ctx context.Context, token, containerName string) (string, error)
}

type DockerInferenceProxy struct {
	Viewers ViewerResolver
}

// AppViewerProxy is the proxy for the app viewer docker inference. The
// upstream host is the container bound to the viewer token, never a value
// taken from the request.
func (proxy *DockerInferenceProxy) AppViewerProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerName := chi.URLParam(r, "containerName")
		viewerToken := chi.URLParam(r, "viewerToken")
		if len(containerName) == 0 || len(viewerToken) == 0 || proxy.Viewers == nil {
			writeViewerError(w, http.StatusUnauthorized, "Unauthorized access.", apiError.UnauthorizedAccess)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeViewerError(w, http.StatusMethodNotAllowed, "Method not allowed.", apiError.InvalidRequestPayload)
			return
		}

		// chi matches the escaped path when one is present.
		rest := chi.URLParam(r, "*")
		if r.URL.RawPath != "" {
			unescaped, err := url.PathUnescape(rest)
			if err != nil {
				writeViewerError(w, http.StatusBadRequest, "Invalid viewer path.", apiError.InvalidRequestPayload)
				return
			}
			rest = unescaped
		}
		upstreamPath := path.Clean(viewerPathPrefix + rest)
		if rest == "" || strings.HasSuffix(rest, "/") {
			upstreamPath += "/"
		}
		if !strings.HasPrefix(upstreamPath, viewerPathPrefix) {
			writeViewerError(w, http.StatusBadRequest, "Invalid viewer path.", apiError.InvalidRequestPayload)
			return
		}

		host, err := proxy.Viewers.ResolveWebAppViewer(r.Context(), viewerToken, containerName)
		if err != nil {
			switch err.Error() {
			case apiError.UnauthorizedAccess:
				writeViewerError(w, http.StatusUnauthorized, "Unauthorized access.", apiError.UnauthorizedAccess)
			case apiError.ForbiddenAccess:
				writeViewerError(w, http.StatusForbidden, "Forbidden access.", apiError.ForbiddenAccess)
			default:
				writeViewerError(w, http.StatusServiceUnavailable, "Model viewer is temporarily unavailable.", err.Error())
			}
			return
		}

		reverseProxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.URL = &url.URL{Scheme: "http", Host: host, Path: upstreamPath, RawQuery: pr.In.URL.RawQuery}
				pr.Out.Host = host
				pr.Out.Header.Del("Authorization")
				pr.Out.Header.Del("Cookie")
				pr.SetXForwarded()
			},
		}
		reverseProxy.ServeHTTP(w, r)
	}
}

func writeViewerError(w http.ResponseWriter, status int, message, errorCode string) {
	response := viewmodels.HTTPResponseVM{
		Status:    status,
		Success:   false,
		Message:   message,
		ErrorCode: errorCode,
	}
	response.JSON(w)
}
