package orthanc

import (
	"bytes"
	"context"
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
// A tenant only reaches its studies (TenantStudies): the ones it retrieved,
// uploaded, or that its ingestion records, an upload label or the install's
// default tenant assign to it. Without TenantStudies no study is reachable.
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

// qidoChunk is the page size the proxy asks Orthanc for while it filters a
// search across the whole store. It stays under Orthanc's LimitFindResults
// (200 on MHI), which would otherwise cut a search short.
const qidoChunk = 100

// qidoMaxScanned bounds how many results of other tenants' studies one search
// may skip over.
const qidoMaxScanned = 100000

// ownership returns the repository's install-wide answers, or nil.
func (proxy *OrthancProxy) ownership() repository.OrthancTenantStudyOwnershipInterface {
	ownership, _ := proxy.TenantStudies.(repository.OrthancTenantStudyOwnershipInterface)
	return ownership
}

// seesEveryStudy reports whether every study of the shared Orthanc is the
// tenant's (the default tenant of a single-tenant install).
func (proxy *OrthancProxy) seesEveryStudy(r *http.Request, tenantID string) bool {
	ownership := proxy.ownership()
	if ownership == nil {
		return false
	}
	every, err := ownership.SeesEveryStudy(r.Context(), tenantID)
	if err != nil {
		log.Printf("[dicom-web] cannot check the owners of the shared Orthanc: %v", err)
		return false
	}
	return every
}

// serveQIDO runs the search in Orthanc and answers only the results of the
// tenant's studies. A search under /studies/{StudyInstanceUID} only matches
// that study, which was checked. A search across the store is sent as is when
// every study is the tenant's; otherwise it is run in pages of qidoChunk and
// limit and offset are applied here, after other studies' results are removed.
func (proxy *OrthancProxy) serveQIDO(w http.ResponseWriter, r *http.Request, targetURL *url.URL, tenantID string, path dicomWebPath) {
	if path.studyInstanceUID != "" {
		if !proxy.tenantRetrievedStudy(w, r, tenantID, path.studyInstanceUID) {
			return
		}
		forward(w, r, targetURL, path)
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
	if proxy.seesEveryStudy(r, tenantID) {
		forward(w, r, targetURL, path)
		return
	}
	query.Del("limit")
	query.Del("offset")

	visible := make([]json.RawMessage, 0)
	want := offset + limit
	scanned := 0
	for upstreamOffset := 0; scanned < qidoMaxScanned; upstreamOffset += qidoChunk {
		query.Set("limit", strconv.Itoa(qidoChunk))
		query.Set("offset", strconv.Itoa(upstreamOffset))
		results, status, err := qidoPage(r, targetURL, path, query)
		if err != nil {
			log.Println("[dicom-web] QIDO-RS request failed:", err)
			writeServerError(w)
			return
		}
		if status != http.StatusOK && status != http.StatusNoContent {
			w.Header().Set("Content-Type", "application/dicom+json")
			w.WriteHeader(status)
			return
		}
		scanned += len(results)

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
		for index, result := range results {
			if _, ok := retrieved[studyOf[index]]; ok {
				visible = append(visible, result)
			}
		}
		if len(results) < qidoChunk || (limit > 0 && len(visible) >= want) {
			break
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

// qidoPage runs one page of a QIDO-RS search in Orthanc.
func qidoPage(r *http.Request, targetURL *url.URL, path dicomWebPath, query url.Values) ([]json.RawMessage, int, error) {
	upstreamURL := *targetURL
	upstreamURL.Path = strings.TrimSuffix(targetURL.Path, "/") + path.String()
	upstreamURL.RawPath = ""
	upstreamURL.RawQuery = query.Encode()

	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	upstream.Header = r.Header.Clone()
	upstream.Header.Del("Authorization")
	upstream.Header.Set("Accept", "application/dicom+json")
	upstream.Header.Del("Accept-Encoding")
	upstream.Header.Set("X-Forwarded-Host", r.Host)
	upstream.Header.Set("X-Forwarded-For", r.RemoteAddr)

	resp, err := http.DefaultClient.Do(upstream)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, resp.StatusCode, nil
	}
	var results []json.RawMessage
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, resp.StatusCode, nil
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, 0, fmt.Errorf("cannot decode QIDO-RS answer: %w", err)
	}
	return results, resp.StatusCode, nil
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

	retrieved, err := proxy.retrievedStudies(r, tenantID, []string{study})
	if err != nil {
		writeServerError(w)
		return
	}
	if _, ok := retrieved[study]; !ok {
		// A new study, which no tenant owns and Orthanc does not hold yet,
		// becomes the uploader's. Anything else stays refused.
		isNew, err := proxy.newStudy(r, targetURL, study)
		if err != nil {
			writeServerError(w)
			return
		}
		if !isNew {
			writeNotFound(w)
			return
		}
	}

	recorder := &statusRecorder{ResponseWriter: w}
	forward(recorder, r, targetURL, dicomWebPath{segments: []string{"studies", study}, studyInstanceUID: study})
	if recorder.status >= 200 && recorder.status < 300 && proxy.TenantStudies != nil {
		// The upload is the tenant's study from now on.
		if err := proxy.TenantStudies.InsertTenantStudy(context.WithoutCancel(r.Context()), tenantID, study); err != nil {
			log.Printf("[dicom-web] cannot record the uploaded study for tenant %s: %v", tenantID, err)
		}
	}
}

// newStudy reports whether no tenant owns the study and Orthanc holds none of it.
func (proxy *OrthancProxy) newStudy(r *http.Request, targetURL *url.URL, study string) (bool, error) {
	ownership := proxy.ownership()
	if ownership == nil {
		return false, nil
	}
	owned, err := ownership.SelectOwnedStudies(r.Context(), []string{study})
	if err != nil {
		log.Printf("[dicom-web] cannot check the owners of an uploaded study: %v", err)
		return false, err
	}
	if len(owned) > 0 {
		return false, nil
	}
	results, status, err := qidoPage(r, targetURL, dicomWebPath{segments: []string{"studies"}}, url.Values{
		"StudyInstanceUID": {study},
		"limit":            {"1"},
	})
	if err != nil {
		log.Printf("[dicom-web] cannot check whether Orthanc holds an uploaded study: %v", err)
		return false, err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return false, fmt.Errorf("orthanc answered %d", status)
	}
	return len(results) == 0, nil
}

// statusRecorder remembers the status Orthanc answered.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status == 0 {
		recorder.status = status
	}
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

func (recorder *statusRecorder) Flush() {
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
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
