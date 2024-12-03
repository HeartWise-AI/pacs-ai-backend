package dockerinference

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

type DockerInferenceProxy struct{}

// AppViewerProxy is the proxy for the app viewer docker inference
func (proxy *DockerInferenceProxy) AppViewerProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerName := chi.URLParam(r, "containerName")
		if len(containerName) == 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Invalid container name.",
				ErrorCode: apiError.InvalidRequestPayload,
			}

			response.JSON(w)
			return
		}

		// construct the target container URL
		target := fmt.Sprintf("http://%s/app/viewer", containerName)
		targetURL, err := url.Parse(target)
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusInternalServerError,
				Success:   false,
				Message:   "Invalid target container URL.",
				ErrorCode: apiError.ServerError,
			}

			response.JSON(w)
			return
		}

		// create the reverse proxy
		reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)

		// modify the request path to strip the prefix
		fmt.Println("BEFORE:", r.URL.Host, r.URL.Path)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/api/proxy/%s/app/viewer", containerName))
		r.Host = targetURL.Host
		fmt.Println("AFTER:", r.URL.Host, r.URL.Path, r.URL.String())
		r.Header.Set("X-Forwarded-Host", r.Host)
		r.Header.Set("X-Forwarded-For", r.RemoteAddr)

		// serve the reverse proxy
		reverseProxy.ServeHTTP(w, r)
	}
}
