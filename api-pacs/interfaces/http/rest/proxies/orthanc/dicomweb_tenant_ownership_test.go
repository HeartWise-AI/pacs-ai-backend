package orthanc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	"api-pacs/module/orthanc/domain/repository"
)

// memoryOwnership follows OrthancTenantStudyRepository's rules: a study is a
// tenant's when the tenant owns it, or when the tenant is the default tenant
// and nobody owns it.
type memoryOwnership struct {
	mu            sync.Mutex
	owners        map[string]map[string]bool
	defaultTenant string
}

func newMemoryOwnership(defaultTenant string) *memoryOwnership {
	return &memoryOwnership{owners: map[string]map[string]bool{}, defaultTenant: defaultTenant}
}

func (store *memoryOwnership) own(tenantID, study string) {
	_ = store.InsertTenantStudy(context.Background(), tenantID, study)
}

func (store *memoryOwnership) InsertTenantStudy(_ context.Context, tenantID, study string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.owners[study] == nil {
		store.owners[study] = map[string]bool{}
	}
	store.owners[study][tenantID] = true
	return nil
}

func (store *memoryOwnership) SelectTenantStudies(_ context.Context, tenantID string, studies []string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []string
	for _, study := range studies {
		owners := store.owners[study]
		if owners[tenantID] || (store.defaultTenant != "" && tenantID == store.defaultTenant && len(owners) == 0) {
			out = append(out, study)
		}
	}
	return out, nil
}

func (store *memoryOwnership) SelectOwnedStudies(_ context.Context, studies []string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []string
	for _, study := range studies {
		if len(store.owners[study]) > 0 {
			out = append(out, study)
		}
	}
	return out, nil
}

func (store *memoryOwnership) SeesEveryStudy(_ context.Context, tenantID string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.defaultTenant == "" || tenantID != store.defaultTenant {
		return false, nil
	}
	for _, owners := range store.owners {
		for owner := range owners {
			if owner != tenantID {
				return false, nil
			}
		}
	}
	return true, nil
}

func (store *memoryOwnership) SelectTenantStudyEvidence(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

var _ repository.OrthancTenantStudyOwnershipInterface = (*memoryOwnership)(nil)

// pagedOrthanc adds what the plugin does on top of sharedOrthanc for these
// tests: limit and offset on QIDO-RS, and thumbnails. It records the limit of
// every search across the store.
type pagedOrthanc struct {
	*sharedOrthanc
	mu          sync.Mutex
	rootQueries []string
}

func (store *pagedOrthanc) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/dicom-web"), "/")
	parts := strings.Split(path, "/")
	if request.Method == http.MethodGet && len(parts) == 1 && (parts[0] == "studies" || parts[0] == "series" || parts[0] == "instances") {
		store.mu.Lock()
		store.rootQueries = append(store.rootQueries, request.URL.RawQuery)
		store.mu.Unlock()
		query := request.URL.Query()
		limit, _ := strconv.Atoi(query.Get("limit"))
		offset, _ := strconv.Atoi(query.Get("offset"))
		query.Del("limit")
		query.Del("offset")
		matched := store.match(query, "", "", "")
		if parts[0] == "studies" {
			matched = onePerStudy(matched)
		}
		if offset < len(matched) {
			matched = matched[offset:]
		} else {
			matched = nil
		}
		if limit > 0 && limit < len(matched) {
			matched = matched[:limit]
		}
		store.writeQIDO(writer, matched)
		return
	}
	if request.Method == http.MethodGet && len(parts) >= 3 && parts[len(parts)-1] == "thumbnail" {
		var study, series, sop string
		study = parts[1]
		if len(parts) >= 5 {
			series = parts[3]
		}
		if len(parts) >= 7 {
			sop = parts[5]
		}
		store.writePixels(writer, store.match(nil, study, series, sop))
		return
	}
	store.sharedOrthanc.ServeHTTP(writer, request)
}

func onePerStudy(instances []cachedInstance) []cachedInstance {
	seen := map[string]bool{}
	var out []cachedInstance
	for _, instance := range instances {
		if !seen[instance.studyUID] {
			seen[instance.studyUID] = true
			out = append(out, instance)
		}
	}
	return out
}

// ownedRouter mounts the proxy with an ownership store, signed in as tenantID.
func ownedRouter(tenantID string, ownership repository.OrthancTenantStudyRepositoryInterface) http.Handler {
	middleware := &iamMiddleware.IAMMiddleware{
		IAMQueryServiceInterface:  &tenantASessionQuery{tenantID: tenantID, userID: "user-" + tenantID, token: "session-" + tenantID},
		UserQueryServiceInterface: &acceptedPolicyQuery{},
	}
	router := chi.NewRouter()
	router.Route("/proxy", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.TokenSessionOrthancProxyAuthGuard)
			r.Use(middleware.PolicyAcceptanceGuard)
			r.Handle("/orthanc/dicom-web/*", (&OrthancProxy{TenantStudies: ownership}).DICOMWebProxy())
		})
	})
	return router
}

func call(t *testing.T, handler http.Handler, tenantID, method, target string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Authorization", "Bearer session-"+tenantID)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func qidoStudies(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var results []map[string]struct {
		Value []json.RawMessage `json:"Value"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &results))
	var studies []string
	for _, result := range results {
		var uid string
		require.NoError(t, json.Unmarshal(result["0020000D"].Value[0], &uid))
		studies = append(studies, uid)
	}
	return studies
}

// ohifRequests are the requests OHIF 3 sends for one study (captured on MHI
// with Playwright: study/series QIDO with the viewer's parameters, series
// metadata, frames) plus the other WADO-RS resources a DICOMweb client may ask
// for: bulk data, rendered and thumbnails at every level.
func ohifRequests(instance cachedInstance) []struct{ name, target string } {
	base := "/proxy/orthanc/dicom-web"
	study := base + "/studies/" + instance.studyUID
	series := study + "/series/" + instance.seriesUID
	sop := series + "/instances/" + instance.sopUID
	return []struct{ name, target string }{
		{"qido study by uid", base + "/studies?StudyInstanceUID=" + instance.studyUID + "&fuzzymatching=false&includefield=00081030%2C00080060&limit=101&offset=0"},
		{"qido studies of the patient", base + "/studies?00100020=" + instance.patientID + "&fuzzymatching=false&includefield=00081030%2C00080060&limit=101&offset=0"},
		{"qido series of the study", study + "/series?includefield=0008103E%2C00080021"},
		{"qido instances of the series", series + "/instances"},
		{"qido root series", base + "/series?StudyInstanceUID=" + instance.studyUID},
		{"qido root instances", base + "/instances?StudyInstanceUID=" + instance.studyUID},
		{"wado study", study},
		{"wado study metadata", study + "/metadata"},
		{"wado series", series},
		{"wado series metadata", series + "/metadata"},
		{"wado instance", sop},
		{"wado instance metadata", sop + "/metadata"},
		{"wado frames", sop + "/frames/1"},
		{"wado frame list", sop + "/frames/1,2"},
		{"wado bulk data", sop + "/bulk/7fe00010"},
		{"rendered study", study + "/rendered"},
		{"rendered series", series + "/rendered"},
		{"rendered instance", sop + "/rendered"},
		{"rendered frame", sop + "/frames/1/rendered"},
		{"thumbnail study", study + "/thumbnail"},
		{"thumbnail series", series + "/thumbnail"},
		{"thumbnail instance", sop + "/thumbnail"},
	}
}

func TestOHIFRequestsServeTheOwnerAndRefuseAMadeUpTenant(t *testing.T) {
	store := &pagedOrthanc{sharedOrthanc: newSharedOrthanc(tenantAInstance, tenantBInstance)}
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	ownership := newMemoryOwnership("") // multi-tenant: no default tenant
	ownership.own("tenant-a", tenantAInstance.studyUID)
	ownership.own("tenant-b", tenantBInstance.studyUID)

	owner := ownedRouter("tenant-a", ownership)
	madeUp := ownedRouter("tenant-made-up-7f3", ownership)

	for _, request := range ohifRequests(tenantAInstance) {
		t.Run(request.name, func(t *testing.T) {
			got := call(t, owner, "tenant-a", http.MethodGet, request.target, nil, "")
			require.Equal(t, http.StatusOK, got.Code, got.Body.String())
			require.Contains(t, got.Body.String(), tenantAInstance.studyUID)
			require.NotContains(t, got.Body.String(), tenantBInstance.patientName)

			foreign := call(t, madeUp, "tenant-made-up-7f3", http.MethodGet, request.target, nil, "")
			require.NotContains(t, foreign.Body.String(), tenantAInstance.patientName)
			require.NotContains(t, foreign.Body.String(), tenantAInstance.pixels)
			if strings.Contains(request.target, "/dicom-web/studies?") || strings.Contains(request.target, "/dicom-web/series?") || strings.Contains(request.target, "/dicom-web/instances?") {
				// a search across the store answers no result
				require.Empty(t, qidoStudies(t, foreign))
				return
			}
			require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
		})
	}
}

func TestDefaultTenantReadsStudiesThatArriveWithoutATenant(t *testing.T) {
	pushed := tenantInstance("30", "PATIENT-PUSHED")
	store := &pagedOrthanc{sharedOrthanc: newSharedOrthanc(pushed, tenantBInstance)}
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	t.Run("single tenant install: default tenant reads it, others do not", func(t *testing.T) {
		ownership := newMemoryOwnership("tenant-a")
		ownership.own("tenant-b", tenantBInstance.studyUID)
		owner := ownedRouter("tenant-a", ownership)
		other := ownedRouter("tenant-made-up-7f3", ownership)
		for _, request := range ohifRequests(pushed) {
			got := call(t, owner, "tenant-a", http.MethodGet, request.target, nil, "")
			require.Equal(t, http.StatusOK, got.Code, "%s: %s", request.name, got.Body.String())
			require.NotContains(t, got.Body.String(), tenantBInstance.patientName, request.name)
			foreign := call(t, other, "tenant-made-up-7f3", http.MethodGet, request.target, nil, "")
			require.NotContains(t, foreign.Body.String(), pushed.patientName, request.name)
		}
		// tenant B's study is not the default tenant's
		got := call(t, owner, "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies/"+tenantBInstance.studyUID+"/metadata", nil, "")
		require.Equal(t, http.StatusNotFound, got.Code)
	})

	t.Run("multi-tenant install: nobody reads it (fail closed)", func(t *testing.T) {
		ownership := newMemoryOwnership("")
		owner := ownedRouter("tenant-a", ownership)
		got := call(t, owner, "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies/"+pushed.studyUID+"/series/"+pushed.seriesUID+"/metadata", nil, "")
		require.Equal(t, http.StatusNotFound, got.Code)
		require.Empty(t, qidoStudies(t, call(t, owner, "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies?limit=101&offset=0", nil, "")))
	})
}

func TestDefaultTenantSearchIsSentToOrthancUnchanged(t *testing.T) {
	studies := []cachedInstance{tenantInstance("41", "P1"), tenantInstance("42", "P2"), tenantInstance("43", "P3")}
	store := &pagedOrthanc{sharedOrthanc: newSharedOrthanc(studies...)}
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	ownership := newMemoryOwnership("tenant-a")
	ownership.own("tenant-a", studies[0].studyUID) // only the default tenant owns studies
	got := qidoStudies(t, call(t, ownedRouter("tenant-a", ownership), "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies?limit=1&offset=1", nil, ""))
	require.Equal(t, []string{studies[1].studyUID}, got)
	require.Equal(t, []string{"limit=1&offset=1"}, store.rootQueries)
}

func TestFilteredSearchPagesPastOtherTenantsStudies(t *testing.T) {
	var seed []cachedInstance
	var own []string
	for index := 0; index < 260; index++ {
		instance := tenantInstance(fmt.Sprintf("5%03d", index), fmt.Sprintf("P%03d", index))
		seed = append(seed, instance)
		if index%97 == 3 { // 3, 100, 197: spread over three Orthanc pages
			own = append(own, instance.studyUID)
		}
	}
	store := &pagedOrthanc{sharedOrthanc: newSharedOrthanc(seed...)}
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	ownership := newMemoryOwnership("tenant-a")
	for _, instance := range seed {
		if !contains(own, instance.studyUID) {
			ownership.own("tenant-b", instance.studyUID)
		}
	}
	for _, study := range own {
		ownership.own("tenant-a", study)
	}
	router := ownedRouter("tenant-a", ownership)

	all := qidoStudies(t, call(t, router, "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies", nil, ""))
	require.Equal(t, own, all)
	page := qidoStudies(t, call(t, router, "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies?limit=2&offset=1", nil, ""))
	require.Equal(t, own[1:3], page)
	for _, query := range store.rootQueries {
		require.Contains(t, query, "limit=100", "every page asked of Orthanc stays under LimitFindResults")
	}
}

func contains(values []string, value string) bool {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	index := sort.SearchStrings(sorted, value)
	return index < len(sorted) && sorted[index] == value
}

func TestUploadedStudyBelongsToTheUploader(t *testing.T) {
	pushed := tenantInstance("60", "PATIENT-PUSHED")
	store := &pagedOrthanc{sharedOrthanc: newSharedOrthanc(pushed)}
	upstream := httptest.NewServer(store)
	defer upstream.Close()
	t.Setenv("ORTHANC_BASE_URL", upstream.URL)

	ownership := newMemoryOwnership("") // multi-tenant
	uploader := ownedRouter("tenant-a", ownership)
	other := ownedRouter("tenant-made-up-7f3", ownership)

	uploaded := tenantInstance("61", "PATIENT-UPLOADED")
	contentType, body := part10STOWBody(uploaded)
	got := call(t, uploader, "tenant-a", http.MethodPost, "/proxy/orthanc/dicom-web/studies", body, contentType)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	require.True(t, store.containsSOP(uploaded.sopUID))

	metadata := "/proxy/orthanc/dicom-web/studies/" + uploaded.studyUID + "/series/" + uploaded.seriesUID + "/metadata"
	require.Equal(t, http.StatusOK, call(t, uploader, "tenant-a", http.MethodGet, metadata, nil, "").Code)
	require.Equal(t, http.StatusNotFound, call(t, other, "tenant-made-up-7f3", http.MethodGet, metadata, nil, "").Code)

	// a second instance into the uploader's study is accepted
	second := uploaded
	second.sopUID = uploaded.studyUID + ".1.2"
	contentType, body = part10STOWBody(second)
	require.Equal(t, http.StatusOK, call(t, uploader, "tenant-a", http.MethodPost, "/proxy/orthanc/dicom-web/studies/"+uploaded.studyUID, body, contentType).Code)

	// another tenant cannot add to it
	third := uploaded
	third.sopUID = uploaded.studyUID + ".1.3"
	contentType, body = part10STOWBody(third)
	require.Equal(t, http.StatusNotFound, call(t, other, "tenant-made-up-7f3", http.MethodPost, "/proxy/orthanc/dicom-web/studies", body, contentType).Code)
	require.False(t, store.containsSOP(third.sopUID))

	// a study already in Orthanc with no owner is not claimed by an upload
	intruder := pushed
	intruder.sopUID = pushed.studyUID + ".1.7"
	contentType, body = part10STOWBody(intruder)
	require.Equal(t, http.StatusNotFound, call(t, uploader, "tenant-a", http.MethodPost, "/proxy/orthanc/dicom-web/studies", body, contentType).Code)
	require.False(t, store.containsSOP(intruder.sopUID))
	require.Equal(t, http.StatusNotFound, call(t, uploader, "tenant-a", http.MethodGet, "/proxy/orthanc/dicom-web/studies/"+pushed.studyUID+"/metadata", nil, "").Code)
}

// part10STOWBody is a STOW-RS body with one Part 10 instance that has a file
// meta group (explicit VR little endian), as a DICOMweb client sends.
func part10STOWBody(instance cachedInstance) (string, []byte) {
	var meta bytes.Buffer
	appendElement(&meta, 0x0002, 0x0010, "UI", "1.2.840.10008.1.2.1")
	file := explicitDICOM(instance)
	var part10 bytes.Buffer
	part10.Write(file[:132])
	part10.Write(meta.Bytes())
	part10.Write(file[132:])

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(map[string][]string{"Content-Type": {"application/dicom"}})
	if err != nil {
		panic(err)
	}
	if _, err := part.Write(part10.Bytes()); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return fmt.Sprintf(`multipart/related; type="application/dicom"; boundary=%s`, writer.Boundary()), body.Bytes()
}
