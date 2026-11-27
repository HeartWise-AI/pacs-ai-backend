package orthanc

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

type OrthancProxy struct{}

// DICOMWebProxy is the proxy for the DICOM web
func (proxy *OrthancProxy) DICOMWebProxy() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// construct the target container URL
		target := fmt.Sprintf("%s/dicom-web", os.Getenv("ORTHANC_BASE_URL"))
		targetURL, err := url.Parse(target)
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusInternalServerError,
				Success:   false,
				Message:   "Invalid target orthanc URL.",
				ErrorCode: apiError.ServerError,
			}

			response.JSON(w)
			return
		}

		// create the reverse proxy
		reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)

		// modify the request path to strip the prefix
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/proxy/orthanc/dicom-web")
		if !staysInDICOMWeb(r.URL.Path) {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Invalid DICOMweb path.",
				ErrorCode: apiError.InvalidRequestPayload,
			}

			response.JSON(w)
			return
		}
		r.URL.Host = targetURL.Host
		r.URL.Scheme = targetURL.Scheme
		r.Header.Set("X-Forwarded-Host", r.Host)
		r.Header.Set("X-Forwarded-For", r.RemoteAddr)
		r.Host = targetURL.Host

		// serve the reverse proxy
		reverseProxy.ServeHTTP(w, r)
	}
}

// staysInDICOMWeb reports whether Orthanc routes the decoded path under
// /dicom-web. Its web server turns backslashes into slashes, stops at a NUL
// byte and removes dot segments, so a path with any of them is refused.
func staysInDICOMWeb(path string) bool {
	if strings.ContainsAny(path, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment != "" && strings.Trim(segment, ".") == "" {
			return false
		}
	}
	return true
}
