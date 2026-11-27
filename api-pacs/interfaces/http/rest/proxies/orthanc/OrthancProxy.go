package orthanc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	dicomUtils "api-pacs/internal/dicom"
	apiError "api-pacs/internal/errors"
	"api-pacs/internal/inputlimits"
	"api-pacs/module/orthanc/domain/repository"
)

// stowHeaderMaxBytes bounds what a STOW-RS request without a study is read
// ahead to find the Study Instance UID of its first instance.
const stowHeaderMaxBytes = 16 * 1024 * 1024

// OrthancProxy serves the DICOMweb API of the Orthanc that all tenants share.
// A tenant only reaches the studies it retrieved (TenantStudies); without
// TenantStudies no study is reachable.
type OrthancProxy struct {
	TenantStudies repository.OrthancTenantStudyRepositoryInterface
}

// dicomWebPath is a DICOMweb path under /studies, /series or /instances.
// Every segment has been checked, and studyInstanceUID is set for the paths
// under /studies/{StudyInstanceUID}.
type dicomWebPath struct {
	segments         []string
	studyInstanceUID string
}

func parseDICOMWebPath(path string) (dicomWebPath, bool) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for _, segment := range segments {
		if !validPathSegment(segment) {
			return dicomWebPath{}, false
		}
	}
	switch {
	case len(segments) == 1 && (segments[0] == "studies" || segments[0] == "series" || segments[0] == "instances"):
		return dicomWebPath{segments: segments}, true
	case len(segments) >= 2 && segments[0] == "studies" && inputlimits.ValidDICOMUID(segments[1]):
		return dicomWebPath{segments: segments, studyInstanceUID: segments[1]}, true
	default:
		return dicomWebPath{}, false
	}
}

// validPathSegment accepts UIDs, keywords, frame lists and bulk data tags:
// letters, digits, dots and commas, and not only dots.
func validPathSegment(segment string) bool {
	if strings.Trim(segment, ".") == "" {
		return false
	}
	for _, character := range segment {
		switch {
		case character >= '0' && character <= '9',
			character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character == '.', character == ',':
		default:
			return false
		}
	}
	return true
}

// isQIDO reports whether the path is a QIDO-RS search: all studies, series or
// instances, the series of a study, or the instances of a series.
func (path dicomWebPath) isQIDO() bool {
	segments := path.segments
	return len(segments) == 1 ||
		(len(segments) == 3 && segments[2] == "series") ||
		(len(segments) == 5 && segments[2] == "series" && segments[4] == "instances")
}

func (path dicomWebPath) String() string {
	return "/" + strings.Join(path.segments, "/")
}

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

		tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

		path, ok := parseDICOMWebPath(strings.TrimPrefix(r.URL.Path, "/proxy/orthanc/dicom-web"))
		if !ok {
			writeNotFound(w)
			return
		}

		switch {
		case r.Method == http.MethodGet && path.isQIDO():
			proxy.serveQIDO(w, r, targetURL, tenantID, path)
		case r.Method == http.MethodGet && path.studyInstanceUID != "":
			if !proxy.tenantRetrievedStudy(w, r, tenantID, path.studyInstanceUID) {
				return
			}
			forward(w, r, targetURL, path)
		case r.Method == http.MethodPost && path.segments[0] == "studies" && len(path.segments) <= 2:
			proxy.serveSTOW(w, r, targetURL, tenantID, path)
		default:
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusMethodNotAllowed,
				Success:   false,
				Message:   "Method not allowed.",
				ErrorCode: apiError.InvalidRequestPayload,
			}

			response.JSON(w)
		}
	}
}

// forward sends the request to Orthanc at the checked path.
func forward(w http.ResponseWriter, r *http.Request, targetURL *url.URL, path dicomWebPath) {
	reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)

	r.URL.Path = path.String()
	r.URL.RawPath = ""
	r.URL.Host = targetURL.Host
	r.URL.Scheme = targetURL.Scheme
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-For", r.RemoteAddr)
	r.Host = targetURL.Host

	reverseProxy.ServeHTTP(w, r)
}

// tenantRetrievedStudy answers 404 and returns false unless the tenant
// retrieved the study.
func (proxy *OrthancProxy) tenantRetrievedStudy(w http.ResponseWriter, r *http.Request, tenantID, studyInstanceUID string) bool {
	retrieved, err := proxy.retrievedStudies(r, tenantID, []string{studyInstanceUID})
	if err != nil {
		writeServerError(w)
		return false
	}
	if _, ok := retrieved[studyInstanceUID]; !ok {
		writeNotFound(w)
		return false
	}
	return true
}

func (proxy *OrthancProxy) retrievedStudies(r *http.Request, tenantID string, studyInstanceUIDs []string) (map[string]struct{}, error) {
	retrieved := map[string]struct{}{}
	if proxy.TenantStudies == nil || len(studyInstanceUIDs) == 0 {
		return retrieved, nil
	}
	studies, err := proxy.TenantStudies.SelectTenantStudies(r.Context(), tenantID, studyInstanceUIDs)
	if err != nil {
		log.Printf("[dicom-web] cannot list the studies of tenant %s: %v", tenantID, err)
		return nil, err
	}
	for _, study := range studies {
		retrieved[study] = struct{}{}
	}
	return retrieved, nil
}

// serveQIDO runs the search in Orthanc and answers only the results of the
// studies the tenant retrieved. Orthanc matches the whole store, so limit and
// offset are applied here, after the results of other studies are removed.
func (proxy *OrthancProxy) serveQIDO(w http.ResponseWriter, r *http.Request, targetURL *url.URL, tenantID string, path dicomWebPath) {
	if path.studyInstanceUID != "" && !proxy.tenantRetrievedStudy(w, r, tenantID, path.studyInstanceUID) {
		return
	}

	query := r.URL.Query()
	limit, limitErr := qidoBound(query.Get("limit"))
	offset, offsetErr := qidoBound(query.Get("offset"))
	if limitErr != nil || offsetErr != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid limit or offset.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}
	query.Del("limit")
	query.Del("offset")

	upstreamURL := *targetURL
	upstreamURL.Path = strings.TrimSuffix(targetURL.Path, "/") + path.String()
	upstreamURL.RawPath = ""
	upstreamURL.RawQuery = query.Encode()

	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL.String(), nil)
	if err != nil {
		writeServerError(w)
		return
	}
	upstream.Header = r.Header.Clone()
	upstream.Header.Set("Accept", "application/dicom+json")
	upstream.Header.Del("Accept-Encoding")
	upstream.Header.Set("X-Forwarded-Host", r.Host)
	upstream.Header.Set("X-Forwarded-For", r.RemoteAddr)

	resp, err := http.DefaultClient.Do(upstream)
	if err != nil {
		log.Println("[dicom-web] QIDO-RS request failed:", err)
		writeServerError(w)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	var results []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Println("[dicom-web] cannot decode QIDO-RS answer:", err)
		writeServerError(w)
		return
	}

	studyOf := make([]string, len(results))
	var studies []string
	seen := map[string]struct{}{}
	for index, result := range results {
		study := resultStudyInstanceUID(result)
		studyOf[index] = study
		if _, duplicate := seen[study]; study != "" && !duplicate {
			seen[study] = struct{}{}
			studies = append(studies, study)
		}
	}
	retrieved, err := proxy.retrievedStudies(r, tenantID, studies)
	if err != nil {
		writeServerError(w)
		return
	}

	visible := make([]json.RawMessage, 0, len(results))
	for index, result := range results {
		if _, ok := retrieved[studyOf[index]]; ok {
			visible = append(visible, result)
		}
	}
	if offset >= len(visible) {
		visible = visible[:0]
	} else {
		visible = visible[offset:]
	}
	if limit > 0 && limit < len(visible) {
		visible = visible[:limit]
	}

	w.Header().Set("Content-Type", "application/dicom+json")
	w.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(visible)
}

func qidoBound(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	bound, err := strconv.ParseUint(value, 10, 31)
	return int(bound), err
}

// resultStudyInstanceUID returns the Study Instance UID (0020000D) of a
// QIDO-RS result in DICOM JSON, or "" when it has none.
func resultStudyInstanceUID(result json.RawMessage) string {
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(result, &attributes); err != nil {
		return ""
	}
	var study struct {
		Value []string `json:"Value"`
	}
	if err := json.Unmarshal(attributes["0020000D"], &study); err != nil || len(study.Value) != 1 {
		return ""
	}
	return study.Value[0]
}

// serveSTOW stores into a study the tenant retrieved. The request is always
// sent to /studies/{StudyInstanceUID}, where Orthanc refuses, without storing
// them, the instances of any other study. A request without a study is sent to
// the study of its first instance.
func (proxy *OrthancProxy) serveSTOW(w http.ResponseWriter, r *http.Request, targetURL *url.URL, tenantID string, path dicomWebPath) {
	study := path.studyInstanceUID
	if study == "" {
		var err error
		study, err = firstSTOWInstanceStudy(r)
		if err != nil || !inputlimits.ValidDICOMUID(study) {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   "Cannot read the study of the first DICOM instance.",
				ErrorCode: apiError.DICOMParseError,
			}

			response.JSON(w)
			return
		}
	}

	if !proxy.tenantRetrievedStudy(w, r, tenantID, study) {
		return
	}
	forward(w, r, targetURL, dicomWebPath{segments: []string{"studies", study}, studyInstanceUID: study})
}

// firstSTOWInstanceStudy reads the Study Instance UID of the first instance of
// a STOW-RS request. What it reads is put back in front of the body, which is
// then forwarded unchanged.
func firstSTOWInstanceStudy(r *http.Request) (string, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	if mediaType != "multipart/related" || params["boundary"] == "" {
		return "", fmt.Errorf("unexpected STOW-RS content type %q", mediaType)
	}

	var head bytes.Buffer
	body := r.Body
	reader := multipart.NewReader(io.TeeReader(io.LimitReader(body, stowHeaderMaxBytes), &head), params["boundary"])
	defer func() {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head.Bytes()), body), body}
	}()

	part, err := reader.NextPart()
	if err != nil {
		return "", err
	}
	uids, err := dicomUtils.ReadPart10UIDs(part)
	if err != nil {
		return "", err
	}
	return uids.StudyInstanceUID, nil
}

func writeNotFound(w http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusNotFound,
		Success:   false,
		Message:   "Not found.",
		ErrorCode: apiError.MissingRecord,
	}
	response.JSON(w)
}

func writeServerError(w http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusInternalServerError,
		Success:   false,
		Message:   "Server error.",
		ErrorCode: apiError.ServerError,
	}
	response.JSON(w)
}
