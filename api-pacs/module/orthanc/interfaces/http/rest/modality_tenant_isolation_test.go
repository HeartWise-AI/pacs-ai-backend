package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	orthancAPI "api-pacs/infrastructures/providers/api/orthanc"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	apiError "api-pacs/internal/errors"
	accessEntity "api-pacs/module/access/domain/entity"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamServiceTypes "api-pacs/module/iam/infrastructure/service/types"
	"api-pacs/module/orthanc/domain/entity"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
	orthancService "api-pacs/module/orthanc/infrastructure/service"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

const (
	sessionTenantID  = "tenant-a"
	sessionUserID    = "user-a"
	ownedModalityID  = "pacs-a"
	otherModalityID  = "pacs-b"
	ownedPatientName = "OWN-PATIENT"
	otherPatientName = "OTHER-PATIENT"
	otherHost        = "10.2.0.8"
	otherAET         = "TENANT_B_PACS"
	ownedStudyUID    = "1.2.840.113619.2.1"
	ownedSeriesUID   = "1.2.840.113619.2.1.1"
)

// modalityEvent is one step of a request. A modality id is authorized only by
// the first successful select for that id whose returned ModalityID is the
// same id. A later select that returns a different ModalityID does not
// authorize the new id, and no Orthanc call or repository write may precede
// that first select.
type modalityEvent struct {
	kind       string
	seq        int
	tenant     string
	requested  string
	returned   string
	ok         bool
	method     string
	path       string
	body       string
	modalityID string
}

type modalityHarness struct {
	t        *testing.T
	router   http.Handler
	server   *httptest.Server
	tenantID string

	mu       sync.Mutex
	seq      int
	events   []modalityEvent
	records  map[string]entity.DICOMModality
	swapped  map[string]bool
	catalogs int
	queries  map[string]storedQuery
}

type storedQuery struct {
	modalityID string
	level      string
	patientID  string
	name       string
	birthDate  string
}

func newModalityHarness(t *testing.T, role string) *modalityHarness {
	t.Helper()
	t.Setenv("ORTHANC_AET", "PACS_AI")

	h := &modalityHarness{
		t:        t,
		tenantID: sessionTenantID,
		records:  map[string]entity.DICOMModality{},
		swapped:  map[string]bool{},
		queries:  map[string]storedQuery{},
	}
	h.records[sessionTenantID+"\x00"+ownedModalityID] = entity.DICOMModality{
		ID: sessionTenantID + ":" + ownedModalityID, TenantID: sessionTenantID, ModalityID: ownedModalityID,
		AET: "TENANT_A_PACS", CFindEnabled: true, CMoveEnabled: true, CStoreEnabled: true,
	}
	h.records["tenant-b\x00"+otherModalityID] = entity.DICOMModality{
		ID: "tenant-b:" + otherModalityID, TenantID: "tenant-b", ModalityID: otherModalityID,
		AET: otherAET, CFindEnabled: true, CMoveEnabled: true, CStoreEnabled: true,
	}

	h.server = httptest.NewServer(http.HandlerFunc(h.handleOrthanc))
	t.Cleanup(h.server.Close)

	queryService := &orthancService.OrthancQueryService{
		OrthancQueryRepositoryInterface:      h,
		OrthancAPIInterface:                  orthancAPI.Init(h.server.URL),
		TenantQueryServiceInterface:          modalityTenantQuery{},
		ElasticsearchCommandServiceInterface: modalityElasticsearch{},
		UserQueryServiceInterface:            modalityUserQuery{},
	}
	commandService := &orthancService.OrthancCommandService{
		OrthancCommandRepositoryInterface:    h,
		OrthancAPIInterface:                  orthancAPI.Init(h.server.URL),
		TenantQueryServiceInterface:          modalityTenantQuery{},
		ElasticsearchCommandServiceInterface: modalityElasticsearch{},
		UserQueryServiceInterface:            modalityUserQuery{},
	}
	queryController := &OrthancQueryController{OrthancQueryServiceInterface: queryService}
	commandController := &OrthancCommandController{OrthancCommandServiceInterface: commandService}

	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: modalitySessionCommand{},
		IAMQueryServiceInterface:   &modalitySessionQuery{role: role},
		UserQueryServiceInterface:  modalityUserQuery{},
		AccessResolver:             modalityAccessResolver{},
	}
	router := chi.NewRouter()
	router.Route("/v1/orthanc", func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		r.Use(middleware.PolicyAcceptanceGuard)

		r.Post("/modality/studies", queryController.FindModalityStudies)
		r.Post("/modality/retrieve", commandController.RetrieveModalityStudy)
		r.Post("/modality/{modalityID}/study/{studyInstanceUID}/series/store", commandController.StoreStudyCustomSeries)
		r.Get("/modalities/list", queryController.ListDICOMModalities)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(accessEntity.PermDicomManage))
			r.Post("/modality/{modalityID}/echo", commandController.TriggerDICOMEchoSCU)
			r.Put("/modality/{modalityID}/update", commandController.UpdateDICOMModality)
			r.Delete("/modality/{modalityID}/remove", commandController.RemoveDICOMModality)
		})
	})
	h.router = router
	return h
}

func (h *modalityHarness) SelectDICOMModalityByModalityID(_ context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := tenantID + "\x00" + modalityID
	record, ok := h.records[key]
	h.pushLocked(modalityEvent{kind: "select", tenant: tenantID, requested: modalityID, returned: record.ModalityID, ok: ok})
	if !ok {
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}
	if !h.swapped[key] {
		h.swapped[key] = true
		swapped := record
		if record.ModalityID == ownedModalityID {
			swapped.ModalityID = otherModalityID
		} else {
			swapped.ModalityID = ownedModalityID
		}
		h.records[key] = swapped
	}
	return record, nil
}

func (h *modalityHarness) SelectLinkedDICOMModalityWithEnabledCStore(context.Context, string, string) (entity.DICOMModality, error) {
	return entity.DICOMModality{}, errors.New(apiError.MissingRecord)
}

func (h *modalityHarness) DeleteDICOMModality(_ context.Context, tenantID, modalityID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pushLocked(modalityEvent{kind: "delete", tenant: tenantID, modalityID: modalityID})
	return nil
}

func (h *modalityHarness) UpsertDICOMModality(_ context.Context, data repositoryTypes.UpsertDICOMModality) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pushLocked(modalityEvent{kind: "upsert", tenant: data.TenantID, modalityID: data.ModalityID, body: data.HostHash})
	return nil
}

func (h *modalityHarness) pushLocked(event modalityEvent) {
	h.seq++
	event.seq = h.seq
	h.events = append(h.events, event)
}

func (h *modalityHarness) handleOrthanc(w http.ResponseWriter, r *http.Request) {
	payload, _ := io.ReadAll(r.Body)
	modalityID := modalityIDFromPath(r.URL.Path)

	h.mu.Lock()
	h.pushLocked(modalityEvent{
		kind: "orthanc", method: r.Method, path: r.URL.RequestURI(), body: string(payload), modalityID: modalityID,
	})
	if r.Method == http.MethodGet && r.URL.Path == "/modalities" {
		h.catalogs++
	}
	catalogs := h.catalogs
	var query storedQuery
	var queryID string
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query") {
		var request struct {
			Level string            `json:"Level"`
			Query map[string]string `json:"Query"`
		}
		_ = json.Unmarshal(payload, &request)
		queryID = "q" + strings.TrimPrefix(modalityID, "pacs-")
		query = storedQuery{
			modalityID: modalityID, level: request.Level,
			patientID: request.Query["PatientID"], name: request.Query["PatientName"], birthDate: request.Query["PatientBirthDate"],
		}
		h.queries[queryID] = query
	}
	if strings.Contains(r.URL.Path, "/answers") {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 {
			query = h.queries[parts[1]]
		}
	}
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/modalities":
		catalog := map[string]any{
			ownedModalityID: modalityConfig("TENANT_A_PACS", "10.1.0.8"),
			otherModalityID: modalityConfig(otherAET, otherHost),
		}
		if catalogs > 1 {
			catalog["pacs-injected"] = modalityConfig("INJECTED_PACS", "10.9.9.9")
		}
		_ = json.NewEncoder(w).Encode(catalog)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query"):
		_ = json.NewEncoder(w).Encode(map[string]string{"ID": queryID, "Path": "/queries/" + queryID})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/answers"):
		_ = json.NewEncoder(w).Encode(h.answers(query))
	case r.Method == http.MethodPost && r.URL.Path == "/tools/find":
		_, _ = w.Write([]byte("[]"))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/move"):
		_ = json.NewEncoder(w).Encode(map[string]string{"ID": "job-1", "Path": "/jobs/job-1"})
	case r.Method == http.MethodPost && r.URL.Path == "/instances":
		_ = json.NewEncoder(w).Encode(map[string]string{"ID": "instance-1", "Status": "Success"})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/store-straight"):
		_ = json.NewEncoder(w).Encode(map[string]string{"SOPClassUID": "1.2.840.10008.5.1.4.1.1.104.1", "SOPInstanceUID": "1.2.3.4.5"})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/echo"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/modalities/"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/modalities/"):
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (h *modalityHarness) answers(query storedQuery) any {
	name, patientID, birth := otherPatientName, "OTHER1", "19700101"
	if query.modalityID == ownedModalityID {
		name, patientID, birth = ownedPatientName, "OWN1", "19800101"
	}
	if query.level == "Series" {
		return []map[string]string{{
			"StudyInstanceUID": ownedStudyUID, "SeriesInstanceUID": ownedSeriesUID,
			"SeriesNumber": "1", "Modality": "XA", "NumberOfSeriesRelatedInstances": "1",
		}}
	}
	return []map[string]string{{
		"PatientName": name, "PatientID": patientID, "PatientBirthDate": birth,
		"StudyInstanceUID": ownedStudyUID, "StudyDate": "20261101", "StudyTime": "120000",
		"QueryRetrieveLevel": "STUDY",
	}}
}

func modalityConfig(aet, host string) map[string]any {
	return map[string]any{
		"AET": aet, "Host": host, "Port": 104, "Timeout": 0, "UseDicomTls": false,
		"AllowEcho": true, "AllowFind": true, "AllowFindWorklist": true, "AllowGet": true,
		"AllowMove": true, "AllowStore": true, "AllowTranscoding": true,
	}
}

func modalityIDFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "modalities" {
		return ""
	}
	id, err := url.PathUnescape(parts[1])
	if err != nil {
		return parts[1]
	}
	return id
}

func (h *modalityHarness) serve(method, path, body, contentType string) *httptest.ResponseRecorder {
	h.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer session")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, request)
	return recorder
}

func (h *modalityHarness) requireNoSideEffects(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, event := range h.events {
		if event.kind == "select" {
			require.Equal(t, h.tenantID, event.tenant, "modality lookup used a tenant other than the session tenant")
			continue
		}
		t.Fatalf("modality operation reached Orthanc or the modality record before the tenant check refused it: %+v", event)
	}
}

func (h *modalityHarness) requireAuthorizedUse(t *testing.T, modalityID string, allowCatalog bool) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()

	authorizedAt := map[string]int{}
	firstReturned := map[string]string{}
	for _, event := range h.events {
		if event.kind == "select" {
			require.Equal(t, h.tenantID, event.tenant, "modality lookup used a tenant other than the session tenant")
			if !event.ok {
				continue
			}
			if _, seen := firstReturned[event.requested]; seen {
				continue
			}
			firstReturned[event.requested] = event.returned
			if event.returned == event.requested {
				authorizedAt[event.requested] = event.seq
			}
			continue
		}
		if allowCatalog && event.kind == "orthanc" && event.method == http.MethodGet && event.modalityID == "" && strings.HasPrefix(event.path, "/modalities?") {
			continue
		}
		used := event.modalityID
		if used == "" {
			used = modalityID
		}
		seq, ok := authorizedAt[used]
		require.Truef(t, ok && seq < event.seq, "use of modality %q was not the modality authorized for this tenant: %+v", used, event)
		require.Equal(t, modalityID, used)
	}
	_, ok := authorizedAt[modalityID]
	require.True(t, ok, "the caller's modality was never authorized")
}

func TestCrossTenantModalityQueryIsRefused(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "patient name", body: `{"modalityId":"pacs-b","patientName":"*"}`},
		{name: "patient id", body: `{"modalityId":"pacs-b","patientId":"OTHER1"}`},
		{name: "birth date", body: `{"modalityId":"pacs-b","patientBirthDate":"19700101"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newModalityHarness(t, iamEntity.UserRole)
			recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/studies", tc.body, "application/json")
			h.requireNoSideEffects(t)
			require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
			require.NotContains(t, recorder.Body.String(), otherPatientName)
		})
	}
}

func TestCrossTenantModalityRetrieveIsRefused(t *testing.T) {
	h := newModalityHarness(t, iamEntity.UserRole)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/retrieve",
		`{"modalityId":"pacs-b","studyInstanceUID":"`+ownedStudyUID+`"}`, "application/json")
	h.requireNoSideEffects(t)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestModalityListOmitsOtherTenants(t *testing.T) {
	h := newModalityHarness(t, iamEntity.UserRole)
	recorder := h.serve(http.MethodGet, "/v1/orthanc/modalities/list", "", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	body := recorder.Body.String()
	require.Contains(t, body, ownedModalityID)
	require.NotContains(t, body, otherModalityID)
	require.NotContains(t, body, otherHost)
	require.NotContains(t, body, otherAET)
	require.NotContains(t, body, "pacs-injected")
	h.requireAuthorizedUse(t, ownedModalityID, true)
}

func TestCrossTenantModalityUpdateIsRefused(t *testing.T) {
	h := newModalityHarness(t, iamEntity.AdminRole)
	recorder := h.serve(http.MethodPut, "/v1/orthanc/modality/"+otherModalityID+"/update",
		`{"aet":"TENANT_B_PACS","host":"10.9.9.9","port":104}`, "application/json")
	h.requireNoSideEffects(t)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestCrossTenantModalityRemoveIsRefused(t *testing.T) {
	h := newModalityHarness(t, iamEntity.AdminRole)
	recorder := h.serve(http.MethodDelete, "/v1/orthanc/modality/"+otherModalityID+"/remove", "", "")
	h.requireNoSideEffects(t)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestCrossTenantModalityEchoIsRefused(t *testing.T) {
	h := newModalityHarness(t, iamEntity.AdminRole)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/"+otherModalityID+"/echo", "", "")
	h.requireNoSideEffects(t)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestCrossTenantModalityStoreIsRefused(t *testing.T) {
	h := newModalityHarness(t, iamEntity.UserRole)
	body, contentType := storeSeriesBody(t)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/"+otherModalityID+"/study/"+ownedStudyUID+"/series/store", body, contentType)
	h.requireNoSideEffects(t)
	// Store-back now needs processing.manage (store_back_authorization_test.go),
	// so a plain USER is refused before the modality lookup.
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, recorder.Code, recorder.Body.String())
}

func TestOwnedModalityQueryUsesTheAuthorizedModality(t *testing.T) {
	h := newModalityHarness(t, iamEntity.UserRole)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/studies",
		`{"modalityId":"pacs-a","patientId":"OWN1"}`, "application/json")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), ownedPatientName)
	require.NotContains(t, recorder.Body.String(), otherPatientName)
	h.requireAuthorizedUse(t, ownedModalityID, false)

	h.mu.Lock()
	defer h.mu.Unlock()
	var sawQuery bool
	for _, event := range h.events {
		if event.kind == "orthanc" && strings.Contains(event.path, "/modalities/"+ownedModalityID+"/query") {
			sawQuery = true
			require.Contains(t, event.body, `"PatientID":"OWN1"`)
			require.NotContains(t, event.path, otherModalityID)
		}
	}
	require.True(t, sawQuery)
}

func TestOwnedModalityRetrieveMovesTheAuthorizedModality(t *testing.T) {
	h := newModalityHarness(t, iamEntity.UserRole)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/retrieve",
		`{"modalityId":"pacs-a","studyInstanceUID":"`+ownedStudyUID+`"}`, "application/json")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	h.requireAuthorizedUse(t, ownedModalityID, false)

	h.mu.Lock()
	defer h.mu.Unlock()
	var sawMove bool
	for _, event := range h.events {
		if event.kind == "orthanc" && event.method == http.MethodPost && strings.HasSuffix(strings.Split(event.path, "?")[0], "/move") {
			sawMove = true
			require.Contains(t, event.path, "/modalities/"+ownedModalityID+"/move")
			require.Contains(t, event.body, ownedStudyUID)
		}
	}
	require.True(t, sawMove)
}

func TestOwnedModalityUpdateChangesTheAuthorizedModality(t *testing.T) {
	h := newModalityHarness(t, iamEntity.AdminRole)
	recorder := h.serve(http.MethodPut, "/v1/orthanc/modality/"+ownedModalityID+"/update",
		`{"aet":"TENANT_A_PACS","host":"10.1.0.9","port":104}`, "application/json")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	h.requireAuthorizedUse(t, ownedModalityID, false)

	h.mu.Lock()
	defer h.mu.Unlock()
	var sawPut bool
	for _, event := range h.events {
		if event.kind == "orthanc" && event.method == http.MethodPut {
			sawPut = true
			require.Equal(t, "/modalities/"+ownedModalityID, strings.Split(event.path, "?")[0])
			require.Contains(t, event.body, "10.1.0.9")
		}
	}
	require.True(t, sawPut)
}

func TestOwnedModalityRemoveDeletesTheAuthorizedModality(t *testing.T) {
	h := newModalityHarness(t, iamEntity.AdminRole)
	recorder := h.serve(http.MethodDelete, "/v1/orthanc/modality/"+ownedModalityID+"/remove", "", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	h.requireAuthorizedUse(t, ownedModalityID, false)
}

func TestOwnedModalityEchoReachesTheAuthorizedModality(t *testing.T) {
	h := newModalityHarness(t, iamEntity.AdminRole)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/"+ownedModalityID+"/echo", "", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	h.requireAuthorizedUse(t, ownedModalityID, false)
}

// A plain USER can no longer store a caller-supplied file to an owned modality:
// store-back sends only registered model-run reports and needs
// processing.manage (store_back_authorization_test.go covers the allowed path).
func TestOwnedModalityStoreOfCallerFileIsRefused(t *testing.T) {
	h := newModalityHarness(t, iamEntity.UserRole)
	body, contentType := storeSeriesBody(t)
	recorder := h.serve(http.MethodPost, "/v1/orthanc/modality/"+ownedModalityID+"/study/"+ownedStudyUID+"/series/store", body, contentType)
	h.requireNoSideEffects(t)
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
}

func storeSeriesBody(t *testing.T) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="report.dcm"`)
	header.Set("Content-Type", "application/dicom")
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write([]byte("DICM"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("seriesInstanceUIDs", `["1.2.840.113619.2.1.1"]`))
	require.NoError(t, writer.WriteField("patientID", "OWN1"))
	require.NoError(t, writer.WriteField("patientName", "OWN-PATIENT"))
	require.NoError(t, writer.WriteField("modelName", "Example"))
	require.NoError(t, writer.WriteField("modelVersion", "1"))
	require.NoError(t, writer.Close())
	return buf.String(), writer.FormDataContentType()
}

type modalitySessionQuery struct {
	iamApplication.IAMQueryServiceInterface
	role string
}

func (service *modalitySessionQuery) GetSessionToken(_ context.Context, token string) (iamEntity.TokenSession, error) {
	if token != "session" {
		return iamEntity.TokenSession{}, errors.New("invalid session")
	}
	return iamEntity.TokenSession{TenantID: sessionTenantID, UserID: sessionUserID, Role: service.role}, nil
}

func (*modalitySessionQuery) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

type modalityAccessResolver struct{}

func (modalityAccessResolver) Resolve(_ context.Context, _, _, role string) (accessEntity.Access, error) {
	return accessEntity.ForRole(role, nil), nil
}

type modalitySessionCommand struct {
	iamApplication.IAMCommandServiceInterface
}

func (modalitySessionCommand) SetTokenSession(context.Context, iamServiceTypes.SetTokenSession) error {
	return nil
}

type modalityUserQuery struct {
	userApplication.UserQueryServiceInterface
}

func (modalityUserQuery) GetPolicyStatus(context.Context, string, string) (userTypes.PolicyStatus, error) {
	return userTypes.PolicyStatus{}, nil
}

func (modalityUserQuery) GetTenantUserByID(context.Context, string, string) (userTypes.GetTenantUser, error) {
	return userTypes.GetTenantUser{ID: sessionUserID, TenantID: sessionTenantID, Email: "user-a@example.com", Name: "User A"}, nil
}

type modalityTenantQuery struct {
	tenantApplication.TenantQueryServiceInterface
}

func (modalityTenantQuery) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{ID: sessionTenantID, Name: "Tenant A"}, nil
}

type modalityElasticsearch struct {
	elasticsearchApplication.ElasticsearchCommandServiceInterface
}

func (modalityElasticsearch) CreateGetModalityStudyLog(context.Context, elasticsearchTypes.CreateGetModalityStudyLog) (*index.Response, error) {
	return nil, nil
}

func (modalityElasticsearch) CreateRetrieveStudyLog(context.Context, elasticsearchTypes.CreateRetrieveStudyLog) (*index.Response, error) {
	return nil, nil
}

func (modalityElasticsearch) CreateStoredCustomSeriesLog(context.Context, elasticsearchTypes.CreateStoredCustomSeriesLog) (*index.Response, error) {
	return nil, nil
}
