package service

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
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/infrastructures/providers/api/orthanc"
	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	apiError "api-pacs/internal/errors"
	hashUtils "api-pacs/internal/hash"
	accessEntity "api-pacs/module/access/domain/entity"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamServiceTypes "api-pacs/module/iam/infrastructure/service/types"
	"api-pacs/module/orthanc/domain/entity"
	orthancRepository "api-pacs/module/orthanc/domain/repository"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
	serviceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	orthancRest "api-pacs/module/orthanc/interfaces/http/rest"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userTypes "api-pacs/module/user/infrastructure/service/types"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
)

const (
	callerTenant       = "tenant-a"
	callerUser         = "user-a"
	sessionPlaceholder = "session"
	localAET           = "LOCAL_AET"

	// victimModality is registered on the shared Orthanc and is not in the
	// caller's tenant directory.
	victimModality = "victim-pacs"
	victimAET      = "VICTIM_AET"
	victimHost     = "pacs.victim.example"

	ownedModality = "tenant-a-pacs"
	ownedAET      = "TENANT_A_AET"
	ownedHost     = "pacs.tenant-a.example"

	// requestedModality is the caller-supplied lookup key. The ownership
	// record names a different Orthanc modality than that string, and a second
	// load of the same key names a third one.
	requestedModality  = "requested-pacs"
	authorizedModality = "authorized-pacs"
	swappedModality    = "swapped-pacs"

	foreignPatient = "FOREIGN-PATIENT-TOKEN"
	ownedPatient   = "OWNED-PATIENT-TOKEN"
	studyUID       = "1.2.826.0.1.3680043.10.511.999"
	seriesUID      = "1.2.826.0.1.3680043.10.511.999.1"
)

// modalityDirectory is the tenant ownership store. Its answers follow
// OrthancQueryRepository.SelectDICOMModalityByModalityID: a missing
// tenantID:modalityID document is an error. When a key is scripted to flip,
// the first load is the authorized record and every later load is a different
// modality, so a check that reads the key again acts on the wrong target.
type modalityDirectory struct {
	mu      sync.Mutex
	scripts map[string]scriptedModality
	upserts []repositoryTypes.UpsertDICOMModality
	deletes []string
	selects []string
}

type scriptedModality struct {
	first    entity.DICOMModality
	firstErr error
	rest     entity.DICOMModality
	restErr  error
	flip     bool
	calls    int
}

func directoryKey(tenantID, modalityID string) string {
	return tenantID + "\x00" + modalityID
}

func (directory *modalityDirectory) SelectDICOMModalityByModalityID(_ context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	directory.selects = append(directory.selects, directoryKey(tenantID, modalityID))
	script, ok := directory.scripts[directoryKey(tenantID, modalityID)]
	if !ok {
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}
	script.calls++
	directory.scripts[directoryKey(tenantID, modalityID)] = script
	if script.flip && script.calls > 1 {
		return script.rest, script.restErr
	}
	return script.first, script.firstErr
}

func (directory *modalityDirectory) SelectLinkedDICOMModalityWithEnabledCStore(context.Context, string, string) (entity.DICOMModality, error) {
	return entity.DICOMModality{}, errors.New(apiError.MissingRecord)
}

func (directory *modalityDirectory) DeleteDICOMModality(_ context.Context, tenantID, modalityID string) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	directory.deletes = append(directory.deletes, directoryKey(tenantID, modalityID))
	return nil
}

func (directory *modalityDirectory) UpsertDICOMModality(_ context.Context, data repositoryTypes.UpsertDICOMModality) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	directory.upserts = append(directory.upserts, data)
	return nil
}

func ownedRecord(modalityID string) entity.DICOMModality {
	return entity.DICOMModality{
		ID:            directoryKey(callerTenant, modalityID),
		TenantID:      callerTenant,
		ModalityID:    modalityID,
		AET:           ownedAET,
		CFindEnabled:  true,
		CMoveEnabled:  true,
		CStoreEnabled: true,
	}
}

func sticky(record entity.DICOMModality, err error) scriptedModality {
	return scriptedModality{first: record, firstErr: err}
}

func flips(first, rest entity.DICOMModality) scriptedModality {
	return scriptedModality{first: first, rest: rest, flip: true}
}

// orthancSpy is the shared Orthanc this process talks to. Responses follow the
// JSON the client in infrastructures/providers/api/orthanc decodes.
type orthancSpy struct {
	mu        sync.Mutex
	calls     []orthancCall
	lastLevel string
}

type orthancCall struct {
	method string
	path   string
	body   []byte
}

func (spy *orthancSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	spy.mu.Lock()
	spy.calls = append(spy.calls, orthancCall{method: r.Method, path: r.URL.Path, body: append([]byte(nil), body...)})
	if bytes.Contains(body, []byte(`"Level":"Series"`)) {
		spy.lastLevel = "Series"
	} else if bytes.Contains(body, []byte(`"Level":"Study"`)) {
		spy.lastLevel = "Study"
	}
	level := spy.lastLevel
	spy.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/modalities":
		_ = json.NewEncoder(w).Encode(map[string]orthancAPITypes.ListDICOMModalitiesResponse{
			victimModality: {AET: victimAET, Host: victimHost, Port: 11112, AllowFind: true, AllowMove: true, AllowStore: true},
			ownedModality:  {AET: ownedAET, Host: ownedHost, Port: 104, AllowFind: true, AllowMove: true, AllowStore: true},
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/query"):
		_ = json.NewEncoder(w).Encode(orthancAPITypes.QueryModalityResponse{ID: "query-1", Path: "/queries/query-1"})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/queries/") && strings.Contains(r.URL.Path, "/answers"):
		if level == "Series" {
			_ = json.NewEncoder(w).Encode([]orthancAPITypes.QueryModalitySeriesAnswersResponse{{
				StudyInstanceUID:               studyUID,
				SeriesInstanceUID:              seriesUID,
				SeriesNumber:                   "1",
				NumberOfSeriesRelatedInstances: "1",
				QueryRetrieveLevel:             "SERIES",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode([]orthancAPITypes.QueryModalityStudyAnswersResponse{{
			PatientID:          "SYNTH-1",
			PatientName:        patientNameForLastQuery(spy),
			StudyInstanceUID:   studyUID,
			StudyDate:          "20260102",
			StudyTime:          "120000",
			QueryRetrieveLevel: "STUDY",
		}})
	case r.Method == http.MethodPost && r.URL.Path == "/tools/find":
		_, _ = w.Write([]byte("[]"))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/move"):
		_ = json.NewEncoder(w).Encode(orthancAPITypes.QueryModalityResponse{ID: "job-1", Path: "/jobs/job-1"})
	case r.Method == http.MethodPost && r.URL.Path == "/instances":
		_ = json.NewEncoder(w).Encode(orthancAPITypes.UploadDICOMInstancesResponse{
			ID: "instance-1", Path: "/instances/instance-1", Status: orthancAPITypes.UploadDICOMStatusSuccess,
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/store-straight"):
		_ = json.NewEncoder(w).Encode(orthancAPITypes.StraightDICOMStoreSCUResponse{
			SOPClassUID: "1.2.840.10008.5.1.4.1.1.104.1", SOPInstanceUID: "1.2.826.0.1.3680043.10.511.50",
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/echo"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/modalities/"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/modalities/"):
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "unexpected orthanc request", http.StatusNotFound)
	}
}

func patientNameForLastQuery(spy *orthancSpy) string {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	for i := len(spy.calls) - 1; i >= 0; i-- {
		call := spy.calls[i]
		if call.method == http.MethodPost && strings.HasSuffix(call.path, "/query") {
			if id, ok := modalityIDFromPath(call.path); ok && id == authorizedModality {
				return ownedPatient
			}
			return foreignPatient
		}
	}
	return foreignPatient
}

func (spy *orthancSpy) modalityIDs() []string {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	ids := []string{}
	for _, call := range spy.calls {
		if id, ok := modalityIDFromPath(call.path); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func (spy *orthancSpy) paths() string {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	parts := make([]string, 0, len(spy.calls))
	for _, call := range spy.calls {
		parts = append(parts, call.method+" "+call.path)
	}
	return strings.Join(parts, ", ")
}

func modalityIDFromPath(path string) (string, bool) {
	const prefix = "/modalities/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(path, prefix), "/")
	if id == "" {
		return "", false
	}
	return id, true
}

type isolationHarness struct {
	server      *httptest.Server
	spy         *orthancSpy
	directory   *modalityDirectory
	querySvc    *OrthancQueryService
	commandSvc  *OrthancCommandService
	queryHTTP   *orthancRest.OrthancQueryController
	commandHTTP *orthancRest.OrthancCommandController
}

func newHarness(t *testing.T, scripts map[string]scriptedModality) *isolationHarness {
	t.Helper()
	t.Setenv("ORTHANC_AET", localAET)
	spy := &orthancSpy{}
	server := httptest.NewServer(spy)
	t.Cleanup(server.Close)
	directory := &modalityDirectory{scripts: scripts}
	api := orthanc.Init(server.URL)
	users := quietUsers{}
	tenants := quietTenants{}
	logs := quietLogs{}
	querySvc := &OrthancQueryService{
		OrthancQueryRepositoryInterface:      directory,
		OrthancAPIInterface:                  api,
		TenantQueryServiceInterface:          tenants,
		ElasticsearchCommandServiceInterface: logs,
		UserQueryServiceInterface:            users,
	}
	commandSvc := &OrthancCommandService{
		OrthancCommandRepositoryInterface:    directory,
		OrthancAPIInterface:                  api,
		TenantQueryServiceInterface:          tenants,
		ElasticsearchCommandServiceInterface: logs,
		UserQueryServiceInterface:            users,
	}
	wireOwnershipLookup(querySvc, directory)
	wireOwnershipLookup(commandSvc, directory)
	return &isolationHarness{
		server: server, spy: spy, directory: directory, querySvc: querySvc, commandSvc: commandSvc,
		queryHTTP:   &orthancRest.OrthancQueryController{OrthancQueryServiceInterface: querySvc},
		commandHTTP: &orthancRest.OrthancCommandController{OrthancCommandServiceInterface: commandSvc},
	}
}

// wireOwnershipLookup attaches the tenant directory to any ownership-lookup
// field added on the service. The field the product has today is set by the
// composite literal; a new exported or unexported interface field is filled here.
func wireOwnershipLookup(target any, lookup orthancRepository.OrthancQueryRepositoryInterface) {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return
	}
	elem := value.Elem()
	if elem.Kind() != reflect.Struct {
		return
	}
	lookupValue := reflect.ValueOf(lookup)
	for i := 0; i < elem.NumField(); i++ {
		field := elem.Field(i)
		if !field.CanSet() || field.Kind() != reflect.Interface || !field.IsNil() {
			continue
		}
		if lookupValue.Type().Implements(field.Type()) {
			field.Set(lookupValue)
		}
	}
}

func denialScripts() map[string]scriptedModality {
	return map[string]scriptedModality{
		directoryKey(callerTenant, ownedModality):  sticky(ownedRecord(ownedModality), nil),
		directoryKey(callerTenant, victimModality): sticky(entity.DICOMModality{}, errors.New(apiError.FirestoreError)),
	}
}

func bindingScripts() map[string]scriptedModality {
	return map[string]scriptedModality{
		directoryKey(callerTenant, requestedModality):  flips(ownedRecord(authorizedModality), ownedRecord(swappedModality)),
		directoryKey(callerTenant, authorizedModality): sticky(ownedRecord(authorizedModality), nil),
		// List reads every Orthanc id. A later load of the caller's modality
		// fails, and a later load of the other tenant's modality succeeds.
		directoryKey(callerTenant, ownedModality): {
			first: ownedRecord(ownedModality), restErr: errors.New(apiError.FirestoreError), flip: true,
		},
		directoryKey(callerTenant, victimModality): {
			firstErr: errors.New(apiError.FirestoreError), rest: ownedRecord(victimModality), flip: true,
		},
	}
}

func (harness *isolationHarness) handler(allowManage bool) http.Handler {
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: sessionCommands{},
		IAMQueryServiceInterface:   sessionQueries{},
		UserQueryServiceInterface:  policyGate{},
		AccessResolver:             accessResolver{allowManage: allowManage},
	}
	router := chi.NewRouter()
	router.Route("/v1/orthanc", func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		r.Use(middleware.PolicyAcceptanceGuard)
		r.Post("/modality/studies", harness.queryHTTP.FindModalityStudies)
		r.Post("/modality/retrieve", harness.commandHTTP.RetrieveModalityStudy)
		r.Post("/modality/{modalityID}/study/{studyInstanceUID}/series/store", harness.commandHTTP.StoreStudyCustomSeries)
		r.Get("/modalities/list", harness.queryHTTP.ListDICOMModalities)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequirePermission(accessEntity.PermDicomManage))
			r.Post("/modality/{modalityID}/echo", harness.commandHTTP.TriggerDICOMEchoSCU)
			r.Put("/modality/{modalityID}/update", harness.commandHTTP.UpdateDICOMModality)
			r.Delete("/modality/{modalityID}/remove", harness.commandHTTP.RemoveDICOMModality)
		})
	})
	return router
}

func (harness *isolationHarness) request(allowManage bool, method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer "+sessionPlaceholder)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	harness.handler(allowManage).ServeHTTP(recorder, req)
	return recorder
}

func requireNoOrthancSideEffect(t *testing.T, recorder *httptest.ResponseRecorder, harness *isolationHarness) {
	t.Helper()
	var payload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Empty(t, harness.spy.calls, "orthanc was called: %s", harness.spy.paths())
	require.Empty(t, harness.directory.upserts)
	require.Empty(t, harness.directory.deletes)
	require.False(t, payload.Success, "body: %s", recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), foreignPatient)
	require.NotContains(t, recorder.Body.String(), victimHost)
	require.NotContains(t, recorder.Body.String(), victimAET)
}

func requireAuthorizedModality(t *testing.T, harness *isolationHarness) {
	t.Helper()
	ids := harness.spy.modalityIDs()
	require.NotEmpty(t, ids, "orthanc was not called: %s", harness.spy.paths())
	for _, id := range ids {
		require.Equal(t, authorizedModality, id, "orthanc calls: %s", harness.spy.paths())
	}
	require.NotContains(t, harness.spy.paths(), victimModality)
	require.NotContains(t, harness.spy.paths(), swappedModality)
	require.NotContains(t, harness.spy.paths(), requestedModality)
}

func TestCrossTenantModalityOperationsDoNotReachOrthanc(t *testing.T) {
	user := callerUser
	findBody := `{"modalityId":"` + victimModality + `","patientId":"SYNTH-1"}`
	retrieveBody := `{"modalityId":"` + victimModality + `","studyInstanceUID":"` + studyUID + `"}`
	updateBody := `{"aet":"ATTACKER_AET","host":"attacker.example","port":104,"cFindEnabled":true,"cMoveEnabled":true,"cStoreEnabled":true}`

	cases := []struct {
		name   string
		manage bool
		method string
		path   string
		body   string
	}{
		{name: "c-find", method: http.MethodPost, path: "/v1/orthanc/modality/studies", body: findBody},
		{name: "c-move", method: http.MethodPost, path: "/v1/orthanc/modality/retrieve", body: retrieveBody},
		{name: "c-echo", manage: true, method: http.MethodPost, path: "/v1/orthanc/modality/" + victimModality + "/echo"},
		{name: "update", manage: true, method: http.MethodPut, path: "/v1/orthanc/modality/" + victimModality + "/update", body: updateBody},
		{name: "delete", manage: true, method: http.MethodDelete, path: "/v1/orthanc/modality/" + victimModality + "/remove"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newHarness(t, denialScripts())
			contentType := ""
			if tc.body != "" {
				contentType = "application/json"
			}
			recorder := harness.request(tc.manage, tc.method, tc.path, contentType, strings.NewReader(tc.body))
			requireNoOrthancSideEffect(t, recorder, harness)
		})
	}

	t.Run("c-store", func(t *testing.T) {
		harness := newHarness(t, denialScripts())
		body, contentType := syntheticStoreBody(t)
		recorder := harness.request(false, http.MethodPost, "/v1/orthanc/modality/"+victimModality+"/study/"+studyUID+"/series/store", contentType, body)
		requireNoOrthancSideEffect(t, recorder, harness)
	})

	t.Run("list", func(t *testing.T) {
		harness := newHarness(t, denialScripts())
		recorder := harness.request(false, http.MethodGet, "/v1/orthanc/modalities/list", "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), ownedHost)
		require.Contains(t, recorder.Body.String(), ownedModality)
		require.NotContains(t, recorder.Body.String(), victimHost)
		require.NotContains(t, recorder.Body.String(), victimAET)
		require.NotContains(t, recorder.Body.String(), victimModality)
	})

	t.Run("ingestion c-find", func(t *testing.T) {
		harness := newHarness(t, denialScripts())
		studies, queryID, err := harness.querySvc.FindModalityStudies(context.Background(), serviceTypes.FindModalityStudies{
			TenantID: callerTenant, ModalityID: victimModality, PatientID: "SYNTH-1", UserID: &user,
		})
		require.Error(t, err)
		require.Empty(t, studies)
		require.Empty(t, queryID)
		require.Empty(t, harness.spy.calls, harness.spy.paths())
	})

	t.Run("ingestion c-move", func(t *testing.T) {
		harness := newHarness(t, denialScripts())
		jobs, err := harness.commandSvc.RetrieveModalityStudyBySeries(context.Background(), serviceTypes.RetrieveModalityStudyBySeries{
			TenantID: callerTenant, ModalityID: victimModality, StudyInstanceUID: studyUID, UserID: &user,
		})
		require.Error(t, err)
		require.Empty(t, jobs)
		require.Empty(t, harness.spy.calls, harness.spy.paths())
	})
}

func TestModalityOperationUsesAuthorizedRecord(t *testing.T) {
	user := callerUser
	findBody := `{"modalityId":"` + requestedModality + `","patientId":"SYNTH-1"}`
	retrieveBody := `{"modalityId":"` + requestedModality + `","studyInstanceUID":"` + studyUID + `"}`
	updateBody := `{"aet":"AUTHORIZED_AET","host":"updated.tenant-a.example","port":104,"cFindEnabled":true,"cMoveEnabled":true,"cStoreEnabled":true}`

	t.Run("c-find", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		recorder := harness.request(false, http.MethodPost, "/v1/orthanc/modality/studies", "application/json", strings.NewReader(findBody))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), ownedPatient)
		require.NotContains(t, recorder.Body.String(), foreignPatient)
		requireAuthorizedModality(t, harness)
	})

	t.Run("ingestion c-find", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		studies, _, err := harness.querySvc.FindModalityStudies(context.Background(), serviceTypes.FindModalityStudies{
			TenantID: callerTenant, ModalityID: requestedModality, PatientID: "SYNTH-1", UserID: &user,
		})
		require.NoError(t, err)
		require.NotEmpty(t, studies)
		require.Equal(t, ownedPatient, studies[0].PatientName)
		requireAuthorizedModality(t, harness)
	})

	t.Run("c-move", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		recorder := harness.request(false, http.MethodPost, "/v1/orthanc/modality/retrieve", "application/json", strings.NewReader(retrieveBody))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		requireAuthorizedModality(t, harness)
		var moved bool
		for _, call := range harness.spy.snapshot() {
			if call.method == http.MethodPost && strings.HasSuffix(call.path, "/move") {
				moved = true
				require.Contains(t, string(call.body), `"TargetAet":"`+localAET+`"`)
				require.Contains(t, string(call.body), studyUID)
			}
		}
		require.True(t, moved, "no C-MOVE was sent: %s", harness.spy.paths())
	})

	t.Run("ingestion c-move", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		jobs, err := harness.commandSvc.RetrieveModalityStudyBySeries(context.Background(), serviceTypes.RetrieveModalityStudyBySeries{
			TenantID: callerTenant, ModalityID: requestedModality, StudyInstanceUID: studyUID, UserID: &user,
		})
		require.NoError(t, err)
		require.NotEmpty(t, jobs)
		requireAuthorizedModality(t, harness)
	})

	t.Run("c-store", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		body, contentType := syntheticStoreBody(t)
		recorder := harness.request(false, http.MethodPost, "/v1/orthanc/modality/"+requestedModality+"/study/"+studyUID+"/series/store", contentType, body)
		require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
		requireAuthorizedModality(t, harness)
		require.Contains(t, harness.spy.paths(), "/store-straight")
	})

	t.Run("c-echo", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		recorder := harness.request(true, http.MethodPost, "/v1/orthanc/modality/"+requestedModality+"/echo", "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		requireAuthorizedModality(t, harness)
		require.Contains(t, harness.spy.paths(), "/echo")
	})

	t.Run("update", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		recorder := harness.request(true, http.MethodPut, "/v1/orthanc/modality/"+requestedModality+"/update", "application/json", strings.NewReader(updateBody))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		requireAuthorizedModality(t, harness)
		var sawHost bool
		for _, call := range harness.spy.snapshot() {
			if call.method == http.MethodPut {
				require.Contains(t, string(call.body), `"Host":"updated.tenant-a.example"`)
				sawHost = true
			}
		}
		require.True(t, sawHost)
		require.Equal(t, []repositoryTypes.UpsertDICOMModality{{
			TenantID: callerTenant, ModalityID: authorizedModality, AET: "AUTHORIZED_AET",
			HostHash: hashUtils.GetMD5Hash("updated.tenant-a.example"), CFindEnabled: true, CMoveEnabled: true, CStoreEnabled: true,
		}}, harness.directory.upserts)
	})

	t.Run("delete", func(t *testing.T) {
		harness := newHarness(t, bindingScripts())
		recorder := harness.request(true, http.MethodDelete, "/v1/orthanc/modality/"+requestedModality+"/remove", "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		requireAuthorizedModality(t, harness)
		require.Equal(t, []string{directoryKey(callerTenant, authorizedModality)}, harness.directory.deletes)
	})

	t.Run("list keeps the first ownership decision", func(t *testing.T) {
		// victim-pacs is absent on the first load and present on a later load.
		// tenant-a-pacs is present on the first load and absent on a later load.
		harness := newHarness(t, bindingScripts())
		recorder := harness.request(false, http.MethodGet, "/v1/orthanc/modalities/list", "", nil)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), ownedHost)
		require.Contains(t, recorder.Body.String(), ownedModality)
		require.NotContains(t, recorder.Body.String(), victimHost)
		require.NotContains(t, recorder.Body.String(), victimAET)
		require.NotContains(t, recorder.Body.String(), victimModality)
	})
}

func TestUserWithoutDicomManageCannotChangeModalities(t *testing.T) {
	harness := newHarness(t, denialScripts())
	recorder := harness.request(false, http.MethodPut, "/v1/orthanc/modality/"+victimModality+"/update", "application/json", strings.NewReader(
		`{"aet":"ATTACKER_AET","host":"attacker.example","port":104}`,
	))
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Empty(t, harness.spy.calls, harness.spy.paths())
	require.Empty(t, harness.directory.upserts)
}

func (spy *orthancSpy) snapshot() []orthancCall {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	out := make([]orthancCall, len(spy.calls))
	copy(out, spy.calls)
	return out
}

func syntheticStoreBody(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="report.dcm"`)
	header.Set("Content-Type", "application/dicom")
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write([]byte("DICM-synthetic"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("seriesInstanceUIDs", `["1.2.826.0.1.3680043.10.511.1"]`))
	require.NoError(t, writer.WriteField("patientID", "SYNTH-1"))
	require.NoError(t, writer.WriteField("patientName", "SYNTH^PATIENT"))
	require.NoError(t, writer.WriteField("modelName", "SyntheticModel"))
	require.NoError(t, writer.WriteField("modelVersion", "0"))
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

type sessionQueries struct {
	iamApplication.IAMQueryServiceInterface
}

func (sessionQueries) GetSessionToken(_ context.Context, token string) (iamEntity.TokenSession, error) {
	if token != sessionPlaceholder {
		return iamEntity.TokenSession{}, errors.New(apiError.UnauthorizedAccess)
	}
	return iamEntity.TokenSession{TenantID: callerTenant, UserID: callerUser, Role: iamEntity.UserRole}, nil
}

func (sessionQueries) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

type sessionCommands struct {
	iamApplication.IAMCommandServiceInterface
}

func (sessionCommands) SetTokenSession(context.Context, iamServiceTypes.SetTokenSession) error {
	return nil
}

type policyGate struct {
	userApplication.UserQueryServiceInterface
}

func (policyGate) GetPolicyStatus(context.Context, string, string) (userTypes.PolicyStatus, error) {
	return userTypes.PolicyStatus{EnforcementActive: true, AcceptanceRequired: false}, nil
}

type accessResolver struct{ allowManage bool }

func (resolver accessResolver) Resolve(context.Context, string, string, string) (accessEntity.Access, error) {
	if resolver.allowManage {
		return accessEntity.ForRole(iamEntity.UserRole, &accessEntity.CustomRole{
			ID: "role-dicom", Name: "dicom", Permissions: []string{accessEntity.PermDicomManage},
		}), nil
	}
	return accessEntity.ForRole(iamEntity.UserRole, nil), nil
}

type quietUsers struct {
	userApplication.UserQueryServiceInterface
}

func (quietUsers) GetTenantUserByID(context.Context, string, string) (userTypes.GetTenantUser, error) {
	return userTypes.GetTenantUser{ID: callerUser, TenantID: callerTenant, Email: "user-a@example.com", Name: "User A"}, nil
}

type quietTenants struct {
	tenantApplication.TenantQueryServiceInterface
}

func (quietTenants) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{ID: callerTenant, Name: "Tenant A"}, nil
}

type quietLogs struct {
	elasticsearchApplication.ElasticsearchCommandServiceInterface
}

func (quietLogs) CreateGetModalityStudyLog(context.Context, elasticsearchTypes.CreateGetModalityStudyLog) (*index.Response, error) {
	return nil, nil
}

func (quietLogs) CreateRetrieveStudyLog(context.Context, elasticsearchTypes.CreateRetrieveStudyLog) (*index.Response, error) {
	return nil, nil
}

func (quietLogs) CreateStoredCustomSeriesLog(context.Context, elasticsearchTypes.CreateStoredCustomSeriesLog) (*index.Response, error) {
	return nil, nil
}
