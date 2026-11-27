package rest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	"api-pacs/infrastructures/providers/api/orthanc"
	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	apiError "api-pacs/internal/errors"
	accessEntity "api-pacs/module/access/domain/entity"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	iamRepositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	iamService "api-pacs/module/iam/infrastructure/service"
	"api-pacs/module/orthanc/domain/entity"
	orthancRepository "api-pacs/module/orthanc/domain/repository"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
	"api-pacs/module/orthanc/infrastructure/service"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

const (
	storeBackTenantID     = "tenant-a"
	storeBackUserID       = "user-a"
	storeBackModalityID   = "hospital-pacs"
	storeBackOtherID      = "other-pacs"
	storeBackSessionToken = "session-token-placeholder"
	forgedPatientID       = "FORGEDPATIENT9F3A"
	forgedPatientName     = "FORGEDNAME9F3A"
	forgedStudyUID        = "1.2.840.113619.2.55.3.999999999"
	forgedModelName       = "ForgedModel"
	forgedModelVersion    = "9.9.9"
	forgedPDFMarker       = "FORGED-PDF-BODY"
	rawDICOMMarker        = "RAW-DICOM-PAYLOAD"
)

// storeBackReport is the model output RegisteredStoreBackReport returns for
// form field executionID. Stored DICOM must carry this report's patient ID,
// patient name, study UID, model name, and PDF bytes. The method signature is
// (ctx, tenantID, executionID) (patientID, patientName, studyInstanceUID,
// modelName, modelVersion string, pdf []byte, seriesInstanceUIDs []string, err error).
type storeBackReport struct {
	ExecutionID        string
	TenantID           string
	PatientID          string
	PatientName        string
	StudyInstanceUID   string
	ModelName          string
	ModelVersion       string
	PDF                []byte
	SeriesInstanceUIDs []string
}

type modalityReply struct {
	record entity.DICOMModality
	err    error
}

type modalityCall struct {
	tenantID   string
	modalityID string
	record     entity.DICOMModality
	err        error
}

// storeBackModalities scripts SelectDICOMModalityByModalityID. A queue longer
// than one entry changes the record on the next lookup of that same key.
type storeBackModalities struct {
	mu       sync.Mutex
	scripted map[string][]modalityReply
	calls    []modalityCall
	report   *storeBackReport
}

func modalityKey(tenantID, modalityID string) string {
	return tenantID + "\x00" + modalityID
}

func (catalog *storeBackModalities) script(tenantID, modalityID string, replies ...modalityReply) {
	if catalog.scripted == nil {
		catalog.scripted = map[string][]modalityReply{}
	}
	catalog.scripted[modalityKey(tenantID, modalityID)] = replies
}

func (catalog *storeBackModalities) SelectDICOMModalityByModalityID(_ context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	key := modalityKey(tenantID, modalityID)
	queue := catalog.scripted[key]
	reply := modalityReply{err: errors.New(apiError.MissingRecord)}
	if len(queue) > 0 {
		reply = queue[0]
		if len(queue) > 1 {
			catalog.scripted[key] = queue[1:]
		}
	}
	catalog.calls = append(catalog.calls, modalityCall{
		tenantID: tenantID, modalityID: modalityID, record: reply.record, err: reply.err,
	})
	return reply.record, reply.err
}

func (catalog *storeBackModalities) SelectLinkedDICOMModalityWithEnabledCStore(context.Context, string, string) (entity.DICOMModality, error) {
	return entity.DICOMModality{}, errors.New(apiError.MissingRecord)
}

func (catalog *storeBackModalities) DeleteDICOMModality(context.Context, string, string) error {
	return nil
}

func (catalog *storeBackModalities) UpsertDICOMModality(context.Context, repositoryTypes.UpsertDICOMModality) error {
	return nil
}

func (catalog *storeBackModalities) RegisteredStoreBackReport(_ context.Context, tenantID, executionID string) (string, string, string, string, string, []byte, []string, error) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.report == nil || catalog.report.TenantID != tenantID || catalog.report.ExecutionID != executionID {
		return "", "", "", "", "", nil, nil, errors.New(apiError.MissingRecord)
	}
	report := catalog.report
	return report.PatientID, report.PatientName, report.StudyInstanceUID, report.ModelName, report.ModelVersion,
		append([]byte(nil), report.PDF...), append([]string(nil), report.SeriesInstanceUIDs...), nil
}

func (catalog *storeBackModalities) snapshot() []modalityCall {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	out := make([]modalityCall, len(catalog.calls))
	copy(out, catalog.calls)
	return out
}

func modalityAuthorized(calls []modalityCall, tenantID, modalityID string) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if call.err != nil || call.tenantID != tenantID || call.modalityID != modalityID {
			return false
		}
		if call.record.TenantID != tenantID || call.record.ModalityID != modalityID || !call.record.CStoreEnabled {
			return false
		}
	}
	return true
}

func enabledModality(tenantID, modalityID string) entity.DICOMModality {
	return entity.DICOMModality{TenantID: tenantID, ModalityID: modalityID, CStoreEnabled: true}
}

type recordedOrthancRequest struct {
	method     string
	path       string
	body       []byte
	authorized bool
}

type orthancRecorder struct {
	mu         sync.Mutex
	catalog    *storeBackModalities
	tenantID   string
	modalityID string
	requests   []recordedOrthancRequest
}

func (recorder *orthancRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusInternalServerError)
		return
	}
	authorized := modalityAuthorized(recorder.catalog.snapshot(), recorder.tenantID, recorder.modalityID)
	recorder.mu.Lock()
	recorder.requests = append(recorder.requests, recordedOrthancRequest{
		method: r.Method, path: r.URL.Path, body: body, authorized: authorized,
	})
	recorder.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/instances":
		_, _ = w.Write([]byte(`{"ID":"uploaded-1","ParentPatient":"p","ParentSeries":"s","ParentStudy":"st","Path":"/instances/uploaded-1","Status":"Success"}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/store-straight"):
		_, _ = w.Write([]byte(`{"SOPClassUID":"1.2.840.10008.5.1.4.1.1.104.1","SOPInstanceUID":"1.2.3.4.5"}`))
	default:
		http.Error(w, "unexpected orthanc request", http.StatusNotFound)
	}
}

func (recorder *orthancRecorder) snapshot() []recordedOrthancRequest {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	out := make([]recordedOrthancRequest, len(recorder.requests))
	copy(out, recorder.requests)
	return out
}

type memoryRedis struct {
	mu     sync.Mutex
	values map[string]string
}

func newMemoryRedis() *memoryRedis {
	return &memoryRedis{values: map[string]string{}}
}

func (redis *memoryRedis) Delete(key string) error {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	delete(redis.values, key)
	return nil
}

func (redis *memoryRedis) DeleteIfValueMatches(string, interface{}) (bool, error) {
	return false, errors.New("not used")
}

func (redis *memoryRedis) Flush() {}

func (redis *memoryRedis) Get(key string) (string, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	value, ok := redis.values[key]
	if !ok {
		return "", errors.New("empty")
	}
	return value, nil
}

func (redis *memoryRedis) GetCounterWithExpiry(string) (int64, time.Duration, error) {
	return 0, 0, errors.New("not used")
}

func (redis *memoryRedis) IncrementWithExpiry(string, time.Duration) (int64, time.Duration, error) {
	return 0, 0, errors.New("not used")
}

func (redis *memoryRedis) Scan(string) ([]string, error) {
	return nil, errors.New("not used")
}

func (redis *memoryRedis) Set(string, interface{}, time.Duration) error {
	return errors.New("not used")
}

func (redis *memoryRedis) SetIfAbsent(string, interface{}, time.Duration) (bool, error) {
	return false, errors.New("not used")
}

// SetIfKeyAbsent matches infrastructures/database/redis SetIfKeyAbsent: when
// blockingKey is absent, key is overwritten.
func (redis *memoryRedis) SetIfKeyAbsent(blockingKey, key string, val interface{}, _ time.Duration) (bool, error) {
	redis.mu.Lock()
	defer redis.mu.Unlock()
	if _, blocked := redis.values[blockingKey]; blocked {
		return false, nil
	}
	switch typed := val.(type) {
	case string:
		redis.values[key] = typed
	case []byte:
		redis.values[key] = string(typed)
	default:
		redis.values[key] = fmt.Sprint(typed)
	}
	return true, nil
}

var _ redisTypes.RedisDBHandlerInterface = (*memoryRedis)(nil)

type storeBackUsers struct {
	userApplication.UserQueryServiceInterface
}

func (storeBackUsers) GetPolicyStatus(context.Context, string, string) (userTypes.PolicyStatus, error) {
	return userTypes.PolicyStatus{EnforcementActive: true, AcceptanceRequired: false}, nil
}

func (storeBackUsers) GetTenantUserByID(_ context.Context, tenantID, id string) (userTypes.GetTenantUser, error) {
	return userTypes.GetTenantUser{ID: id, TenantID: tenantID, Email: "user@example.com", Name: "Ordinary User"}, nil
}

type storeBackTenants struct {
	tenantApplication.TenantQueryServiceInterface
}

func (storeBackTenants) GetTenantByID(_ context.Context, tenantID string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{ID: tenantID, Name: "Tenant A"}, nil
}

type storeBackAudit struct {
	elasticsearchApplication.ElasticsearchCommandServiceInterface
}

func (storeBackAudit) CreateStoredCustomSeriesLog(context.Context, elasticsearchTypes.CreateStoredCustomSeriesLog) (*index.Response, error) {
	return nil, nil
}

type storeBackAccess struct {
	custom *accessEntity.CustomRole
}

func (resolver storeBackAccess) Resolve(_ context.Context, _, _, role string) (accessEntity.Access, error) {
	return accessEntity.ForRole(role, resolver.custom), nil
}

type storeBackHarness struct {
	handler    http.Handler
	recorder   *orthancRecorder
	modalities *storeBackModalities
	role       string
}

func newStoreBackHarness(t *testing.T, role string, custom *accessEntity.CustomRole, modalities *storeBackModalities) *storeBackHarness {
	t.Helper()
	if modalities == nil {
		modalities = &storeBackModalities{}
	}
	recorder := &orthancRecorder{catalog: modalities, tenantID: storeBackTenantID, modalityID: storeBackModalityID}
	server := httptest.NewServer(recorder)
	t.Cleanup(server.Close)

	redis := newMemoryRedis()
	commandRepository := &iamRepository.IAMCommandRepository{RedisDBHandlerInterface: redis}
	require.NoError(t, commandRepository.SetTokenSession(iamRepositoryTypes.SetTokenSession{
		SessionID: storeBackSessionToken, TenantID: storeBackTenantID, UserID: storeBackUserID,
		Role: role, ExpireTimeInSeconds: iamEntity.ExpireTimeInSeconds,
	}))
	queryService := &iamService.IAMQueryService{
		IAMQueryRepositoryInterface: &iamRepository.IAMQueryRepository{RedisDBHandlerInterface: redis},
	}
	session, err := queryService.GetSessionToken(context.Background(), storeBackSessionToken)
	require.NoError(t, err)
	require.Equal(t, role, session.Role)
	require.Equal(t, storeBackTenantID, session.TenantID)

	users := storeBackUsers{}
	commandService := &service.OrthancCommandService{
		OrthancCommandRepositoryInterface:    modalities,
		OrthancAPIInterface:                  orthanc.Init(server.URL),
		TenantQueryServiceInterface:          storeBackTenants{},
		ElasticsearchCommandServiceInterface: storeBackAudit{},
		UserQueryServiceInterface:            users,
	}
	attachStoreBackCollaborators(commandService, modalities)
	controller := &OrthancCommandController{OrthancCommandServiceInterface: commandService}
	attachStoreBackCollaborators(controller, modalities)

	middleware := iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &iamService.IAMCommandService{IAMCommandRepositoryInterface: commandRepository},
		IAMQueryServiceInterface:   queryService,
		UserQueryServiceInterface:  users,
		AccessResolver:             storeBackAccess{custom: custom},
	}
	router := chi.NewRouter()
	router.Route("/v1/orthanc", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			r.Use(middleware.PolicyAcceptanceGuard)
			r.Post("/modality/{modalityID}/study/{studyInstanceUID}/series/store", controller.StoreStudyCustomSeries)
		})
	})
	return &storeBackHarness{handler: router, recorder: recorder, modalities: modalities, role: role}
}

func attachStoreBackCollaborators(target interface{}, modalities *storeBackModalities) {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return
	}
	element := value.Elem()
	if element.Kind() != reflect.Struct {
		return
	}
	catalogValue := reflect.ValueOf(modalities)
	queryRepository := reflect.TypeOf((*orthancRepository.OrthancQueryRepositoryInterface)(nil)).Elem()
	for i := 0; i < element.NumField(); i++ {
		field := element.Field(i)
		if !field.CanSet() || field.Kind() != reflect.Interface {
			continue
		}
		fieldType := field.Type()
		if fieldType == queryRepository || (catalogValue.Type().Implements(fieldType) && fieldType.NumMethod() > 0 && field.IsNil()) {
			if catalogValue.Type().Implements(fieldType) {
				field.Set(catalogValue)
			}
		}
	}
}

func postStoreBack(t *testing.T, handler http.Handler, modalityID, studyUID, mime, executionID string, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="series.bin"`)
	header.Set("Content-Type", mime)
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(file)
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("seriesInstanceUIDs", `["9.9.9.1"]`))
	require.NoError(t, writer.WriteField("patientID", forgedPatientID))
	require.NoError(t, writer.WriteField("patientName", forgedPatientName))
	require.NoError(t, writer.WriteField("modelName", forgedModelName))
	require.NoError(t, writer.WriteField("modelVersion", forgedModelVersion))
	require.NoError(t, writer.WriteField("executionID", executionID))
	require.NoError(t, writer.Close())

	target := "/v1/orthanc/modality/" + modalityID + "/study/" + studyUID + "/series/store"
	request := httptest.NewRequest(http.MethodPost, target, body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+storeBackSessionToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func rawDICOM() []byte {
	return append([]byte{0xff, 0xd8, 0x00}, []byte(rawDICOMMarker)...)
}

func forgedPDF() []byte {
	return []byte("%PDF-1.1\n" + forgedPDFMarker + "\n%%EOF\n")
}

func requireNoOrthancWrite(t *testing.T, recorder *httptest.ResponseRecorder, orthancLog *orthancRecorder) {
	t.Helper()
	requests := orthancLog.snapshot()
	if len(requests) > 0 {
		parts := make([]string, 0, len(requests))
		for _, request := range requests {
			parts = append(parts, fmt.Sprintf("%s %s authorized=%t forged=%t raw=%t",
				request.method, request.path, request.authorized,
				bytes.Contains(request.body, []byte(forgedPatientID)),
				bytes.Contains(request.body, []byte(rawDICOMMarker))))
		}
		t.Fatalf("Orthanc was written before the store was refused:\n%s\nresponse %d %s",
			strings.Join(parts, "\n"), recorder.Code, recorder.Body.String())
	}
	switch recorder.Code {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound:
	default:
		t.Fatalf("status %d, want a refusal, body %s", recorder.Code, recorder.Body.String())
	}
}

func newRegisteredReport(t *testing.T) *storeBackReport {
	t.Helper()
	var buf [8]byte
	_, err := rand.Read(buf[:])
	require.NoError(t, err)
	token := hex.EncodeToString(buf[:])
	return &storeBackReport{
		ExecutionID:        "exec-" + token,
		TenantID:           storeBackTenantID,
		PatientID:          "RP" + token,
		PatientName:        "RN" + token,
		StudyInstanceUID:   "1.2.826.0.1.3680043.10.512." + fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000),
		ModelName:          "CathEF",
		ModelVersion:       "1.0.0",
		PDF:                []byte("%PDF-1.1\nREGISTERED-PDF-" + token + "\n%%EOF\n"),
		SeriesInstanceUIDs: []string{"1.2.840.113619.2.55.3.4"},
	}
}

func requireRegisteredStore(t *testing.T, recorder *httptest.ResponseRecorder, orthancLog *orthancRecorder, report *storeBackReport) {
	t.Helper()
	requests := orthancLog.snapshot()
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d, want the registered report stored, body %s", recorder.Code, recorder.Body.String())
	}
	if len(requests) == 0 {
		t.Fatal("registered report was not sent to Orthanc")
	}
	var stored bool
	for _, request := range requests {
		if !request.authorized {
			t.Fatalf("Orthanc %s %s ran before the modality lookup authorized %s", request.method, request.path, storeBackModalityID)
		}
		if bytes.Contains(request.body, []byte(forgedPatientID)) || bytes.Contains(request.body, []byte(forgedPatientName)) ||
			bytes.Contains(request.body, []byte(forgedStudyUID)) || bytes.Contains(request.body, []byte(forgedModelName)) ||
			bytes.Contains(request.body, []byte(forgedPDFMarker)) || bytes.Contains(request.body, []byte(rawDICOMMarker)) {
			t.Fatalf("Orthanc %s %s carried caller-supplied identity or file bytes", request.method, request.path)
		}
		if request.method == http.MethodPost && request.path == "/modalities/"+storeBackModalityID+"/store-straight" {
			stored = true
			if !bytes.Contains(request.body, []byte(report.PatientID)) || !bytes.Contains(request.body, []byte(report.PatientName)) ||
				!bytes.Contains(request.body, []byte(report.StudyInstanceUID)) || !bytes.Contains(request.body, []byte(report.PDF)) ||
				!bytes.Contains(request.body, []byte(report.ModelName+"-"+report.ModelVersion)) {
				t.Fatalf("C-STORE body is not the registered report")
			}
		}
		if request.method == http.MethodPost && strings.HasSuffix(request.path, "/store-straight") &&
			request.path != "/modalities/"+storeBackModalityID+"/store-straight" {
			t.Fatalf("C-STORE targeted %s", request.path)
		}
	}
	if !stored {
		t.Fatalf("no C-STORE to %s", storeBackModalityID)
	}
}

func TestStoreBackRefusesUnauthorizedWritesBeforeOrthanc(t *testing.T) {
	dicomManage := &accessEntity.CustomRole{
		ID: "role-dicom", Name: "DICOM admins", Permissions: []string{accessEntity.PermDicomManage}, AllModels: true,
	}
	enabled := modalityReply{record: enabledModality(storeBackTenantID, storeBackModalityID)}
	disabled := modalityReply{record: entity.DICOMModality{
		TenantID: storeBackTenantID, ModalityID: storeBackModalityID, CStoreEnabled: false,
	}}
	mismatched := modalityReply{record: enabledModality(storeBackTenantID, storeBackOtherID)}

	tests := []struct {
		name     string
		role     string
		custom   *accessEntity.CustomRole
		modality string
		mime     string
		file     []byte
		replies  []modalityReply
	}{
		{name: "ordinary user raw dicom", role: iamEntity.UserRole, modality: storeBackModalityID, mime: "application/dicom", file: rawDICOM(), replies: []modalityReply{enabled}},
		{name: "ordinary user pdf", role: iamEntity.UserRole, modality: storeBackModalityID, mime: "application/pdf", file: forgedPDF(), replies: []modalityReply{enabled}},
		{name: "dicom.manage user raw dicom", role: iamEntity.UserRole, custom: dicomManage, modality: storeBackModalityID, mime: "application/dicom", file: rawDICOM(), replies: []modalityReply{enabled}},
		{name: "owner raw dicom", role: iamEntity.OwnerRole, modality: storeBackModalityID, mime: "application/dicom", file: rawDICOM(), replies: []modalityReply{enabled}},
		{name: "admin raw dicom", role: iamEntity.AdminRole, modality: storeBackModalityID, mime: "application/dicom", file: rawDICOM(), replies: []modalityReply{enabled}},
		{name: "owner pdf when c-store disabled", role: iamEntity.OwnerRole, modality: storeBackModalityID, mime: "application/pdf", file: forgedPDF(), replies: []modalityReply{disabled}},
		{name: "owner pdf for unknown modality", role: iamEntity.OwnerRole, modality: storeBackModalityID, mime: "application/pdf", file: forgedPDF()},
		{name: "owner pdf when record id differs", role: iamEntity.OwnerRole, modality: storeBackModalityID, mime: "application/pdf", file: forgedPDF(), replies: []modalityReply{mismatched}},
		{name: "owner pdf without a registered report", role: iamEntity.OwnerRole, modality: storeBackModalityID, mime: "application/pdf", file: forgedPDF(), replies: []modalityReply{enabled}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &storeBackModalities{}
			if len(test.replies) > 0 {
				catalog.script(storeBackTenantID, test.modality, test.replies...)
			}
			harness := newStoreBackHarness(t, test.role, test.custom, catalog)
			response := postStoreBack(t, harness.handler, test.modality, forgedStudyUID, test.mime, "exec-unknown", test.file)
			requireNoOrthancWrite(t, response, harness.recorder)
		})
	}
}

func TestStoreBackSendsRegisteredReportOnlyToTheAuthorizedModality(t *testing.T) {
	report := newRegisteredReport(t)
	catalog := &storeBackModalities{report: report}
	catalog.script(storeBackTenantID, storeBackModalityID, modalityReply{record: enabledModality(storeBackTenantID, storeBackModalityID)})
	harness := newStoreBackHarness(t, iamEntity.OwnerRole, nil, catalog)
	response := postStoreBack(t, harness.handler, storeBackModalityID, forgedStudyUID, "application/pdf", report.ExecutionID, forgedPDF())
	requireRegisteredStore(t, response, harness.recorder, report)
}

func TestSecondModalityLookupCannotRetargetStoreBack(t *testing.T) {
	report := newRegisteredReport(t)
	catalog := &storeBackModalities{report: report}
	catalog.script(storeBackTenantID, storeBackModalityID,
		modalityReply{record: enabledModality(storeBackTenantID, storeBackModalityID)},
		modalityReply{record: enabledModality(storeBackTenantID, storeBackOtherID)},
	)
	harness := newStoreBackHarness(t, iamEntity.OwnerRole, nil, catalog)
	response := postStoreBack(t, harness.handler, storeBackModalityID, forgedStudyUID, "application/pdf", report.ExecutionID, forgedPDF())

	requests := harness.recorder.snapshot()
	if len(requests) == 0 {
		switch response.Code {
		case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound:
			return
		default:
			t.Fatalf("status %d with no Orthanc write, body %s", response.Code, response.Body.String())
		}
	}
	requireRegisteredStore(t, response, harness.recorder, report)
}
