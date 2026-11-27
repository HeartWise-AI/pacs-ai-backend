package rest

import (
	"api-pacs/module/access/accessctx"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type viewerRunCommandService struct {
	application.InferenceCommandServiceInterface
	input  serviceTypes.CreateViewerModelRun
	result serviceTypes.CreateViewerModelRunResult
	err    error
}

func (service *viewerRunCommandService) CreateViewerModelRun(_ context.Context, data serviceTypes.CreateViewerModelRun) (serviceTypes.CreateViewerModelRunResult, error) {
	service.input = data
	return service.result, service.err
}

type viewerResultsQueryService struct {
	application.InferenceQueryServiceInterface
	latestInput serviceTypes.GetStudyLatestModelResults
	latest      serviceTypes.StudyLatestModelResults
	reportInput serviceTypes.GetProcessingRunExecutionReport
	jobInput    serviceTypes.GetStudyJobReport
	resultInput serviceTypes.GetStudyJobResult
	jobResult   serviceTypes.StudyJobResult
	report      serviceTypes.ModelReport
	err         error
}

func (service *viewerResultsQueryService) GetStudyLatestModelResults(_ context.Context, data serviceTypes.GetStudyLatestModelResults) (serviceTypes.StudyLatestModelResults, error) {
	service.latestInput = data
	return service.latest, service.err
}

func (service *viewerResultsQueryService) GetProcessingRunExecutionReport(_ context.Context, data serviceTypes.GetProcessingRunExecutionReport) (serviceTypes.ModelReport, error) {
	service.reportInput = data
	return service.report, service.err
}

func (service *viewerResultsQueryService) GetStudyJobReport(_ context.Context, data serviceTypes.GetStudyJobReport) (serviceTypes.ModelReport, error) {
	service.jobInput = data
	return service.report, service.err
}

func (service *viewerResultsQueryService) GetStudyJobResult(_ context.Context, data serviceTypes.GetStudyJobResult) (serviceTypes.StudyJobResult, error) {
	service.resultInput = data
	return service.jobResult, service.err
}

func viewerRequest(method, target, body string, params map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	routeContext := chi.NewRouteContext()
	for key, value := range params {
		routeContext.URLParams.Add(key, value)
	}
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	ctx = context.WithValue(ctx, iamTypes.RoleCtx, "USER")
	return request.WithContext(ctx)
}

func TestRunStudyModelCreatesViewerRun(t *testing.T) {
	service := &viewerRunCommandService{result: serviceTypes.CreateViewerModelRunResult{
		Run: entity.InferenceIngestionProcessingRun{
			ID: "run-5", RunNumber: 5, RunTrigger: entity.InferenceIngestionProcessingRunTriggerViewerRequest,
			Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
		},
		Execution: entity.InferenceIngestionProcessingJob{ID: "execution-5"},
		ModelID:   "registration-1", ModelName: "DeepCORO-JEPA", ModelVersion: "1.3.0",
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.RunStudyModel(recorder, viewerRequest(http.MethodPost, "/run", `{"seriesInstanceUIDs":["1.2.3.4"],"modality":"XA"}`,
		map[string]string{"studyInstanceUID": "1.2.3", "modelId": "registration-1"}))

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, serviceTypes.CreateViewerModelRun{
		TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: "1.2.3", ModelRef: "registration-1",
		SeriesInstanceUIDs: []string{"1.2.3.4"}, Modality: "XA",
	}, service.input)
	require.JSONEq(t, `{"runId":"run-5","runNumber":5,"trigger":"VIEWER_REQUEST","phase":"QUEUED","executionId":"execution-5","modelId":"registration-1","modelName":"DeepCORO-JEPA","modelVersion":"1.3.0"}`,
		string(decodeViewerData(t, recorder)))
}

func TestRunStudyModelAcceptsEmptyBodyAndRejectsUnknownFields(t *testing.T) {
	service := &viewerRunCommandService{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	params := map[string]string{"studyInstanceUID": "1.2.3", "modelId": "registration-1"}

	recorder := httptest.NewRecorder()
	controller.RunStudyModel(recorder, viewerRequest(http.MethodPost, "/run", "", params))
	require.Equal(t, http.StatusCreated, recorder.Code)

	recorder = httptest.NewRecorder()
	controller.RunStudyModel(recorder, viewerRequest(http.MethodPost, "/run", `{"tenantId":"tenant-b"}`, params))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestRunStudyModelMapsErrors(t *testing.T) {
	for code, status := range map[string]int{
		apiError.RunAlreadyActive:          http.StatusConflict,
		apiError.StudyNotAvailableLocally:  http.StatusConflict,
		apiError.MissingRecord:             http.StatusNotFound,
		apiError.InvalidPayload:            http.StatusBadRequest,
		apiError.InferenceModelUnavailable: http.StatusServiceUnavailable,
		apiError.InferenceQuotaUnavailable: http.StatusServiceUnavailable,
	} {
		service := &viewerRunCommandService{err: errors.New(code)}
		controller := InferenceCommandController{InferenceCommandServiceInterface: service}
		recorder := httptest.NewRecorder()
		controller.RunStudyModel(recorder, viewerRequest(http.MethodPost, "/run", "", map[string]string{"studyInstanceUID": "1.2.3", "modelId": "m"}))
		require.Equal(t, status, recorder.Code, code)
		require.Contains(t, recorder.Body.String(), `"errorCode":"`+code+`"`)
	}

	service := &viewerRunCommandService{err: &apiError.InferenceQuotaLimitError{ErrorCode: apiError.InferenceQuotaExceeded}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.RunStudyModel(recorder, viewerRequest(http.MethodPost, "/run", "", map[string]string{"studyInstanceUID": "1.2.3", "modelId": "m"}))
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
}

func TestGetStudyLatestModelResultsPassesTenantStudyAndModality(t *testing.T) {
	service := &viewerResultsQueryService{latest: serviceTypes.StudyLatestModelResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyLatestModelResult{}}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetStudyLatestModelResults(recorder, viewerRequest(http.MethodGet, "/latest-results?modality=angiogram", "", map[string]string{"studyInstanceUID": "1.2.3"}))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, serviceTypes.GetStudyLatestModelResults{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Modality: "angiogram"}, service.latestInput)
	require.JSONEq(t, `{"studyInstanceUID":"1.2.3","results":[]}`, string(decodeViewerData(t, recorder)))
}

func TestGetStudyLatestModelResultsReturns503WhenStudyServiceIsDown(t *testing.T) {
	service := &viewerResultsQueryService{err: errors.New(apiError.InferenceResultServiceUnavailable)}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.GetStudyLatestModelResults(recorder, viewerRequest(http.MethodGet, "/latest-results", "", map[string]string{"studyInstanceUID": "1.2.3"}))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestReportEndpointsReturnHTMLAndMapErrors(t *testing.T) {
	service := &viewerResultsQueryService{report: serviceTypes.ModelReport{HTMLBase64: "PGgxPg==", ContentType: "text/html"}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}

	recorder := httptest.NewRecorder()
	controller.GetProcessingRunExecutionReport(recorder, viewerRequest(http.MethodGet, "/report", "", map[string]string{"runId": "run-1", "executionId": "execution-1"}))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, serviceTypes.GetProcessingRunExecutionReport{TenantID: "tenant-a", RunID: "run-1", ExecutionID: "execution-1"}, service.reportInput)
	require.JSONEq(t, `{"htmlBase64":"PGgxPg==","contentType":"text/html"}`, string(decodeViewerData(t, recorder)))

	recorder = httptest.NewRecorder()
	controller.GetStudyJobReport(recorder, viewerRequest(http.MethodGet, "/report", "", map[string]string{"studyInstanceUID": "1.2.3", "studyServiceJobId": "job-1"}))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, serviceTypes.GetStudyJobReport{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-1"}, service.jobInput)

	for code, status := range map[string]int{
		apiError.ReportNotAvailable:                   http.StatusNotFound,
		apiError.InferenceExecutionResultNotAvailable: http.StatusConflict,
		apiError.ReportRenderRejected:                 http.StatusUnprocessableEntity,
		apiError.ReportRenderFailed:                   http.StatusBadGateway,
	} {
		service.err = errors.New(code)
		recorder = httptest.NewRecorder()
		controller.GetProcessingRunExecutionReport(recorder, viewerRequest(http.MethodGet, "/report", "", map[string]string{"runId": "r", "executionId": "e"}))
		require.Equal(t, status, recorder.Code, code)
		require.Contains(t, recorder.Body.String(), `"errorCode":"`+code+`"`)
	}
}

func TestViewerEndpointsRequireTenant(t *testing.T) {
	controller := InferenceQueryController{InferenceQueryServiceInterface: &viewerResultsQueryService{}}
	recorder := httptest.NewRecorder()
	controller.GetStudyLatestModelResults(recorder, httptest.NewRequest(http.MethodGet, "/latest-results", nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func decodeViewerData(t *testing.T, recorder *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestRunStudyModelDeniesModelsOutsideTheCustomRole(t *testing.T) {
	service := &viewerRunCommandService{}
	controller := InferenceCommandController{
		InferenceCommandServiceInterface: service,
		ModelArtifacts:                   &stubArtifactResolver{ids: map[string][]string{"registration-1": {"DeepCORO_JEPA"}}},
	}
	request := viewerRequest(http.MethodPost, "/run", `{}`, map[string]string{"studyInstanceUID": "1.2.3", "modelId": "registration-1"})
	request = request.WithContext(accessctx.WithAccess(request.Context(), restrictedAccess("PanEcho")))
	recorder := httptest.NewRecorder()

	controller.RunStudyModel(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Equal(t, serviceTypes.CreateViewerModelRun{}, service.input, "no run is created")
}

func TestGetStudyJobResultReturnsStoredJSONAndMapsErrors(t *testing.T) {
	version := "1.0.0"
	service := &viewerResultsQueryService{jobResult: serviceTypes.StudyJobResult{
		StudyServiceJobID: "job-1", StudyInstanceUID: "1.2.3", ModelName: "echo-sr-measurements",
		ModelVersion: &version, Status: "completed", Result: json.RawMessage(`{"summary":{}}`),
	}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	params := map[string]string{"studyInstanceUID": "1.2.3", "studyServiceJobId": "job-1"}

	recorder := httptest.NewRecorder()
	controller.GetStudyJobResult(recorder, viewerRequest(http.MethodGet, "/result", "", params))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "tenant-a", service.resultInput.TenantID)
	require.Equal(t, "1.2.3", service.resultInput.StudyInstanceUID)
	require.Equal(t, "job-1", service.resultInput.StudyServiceJobID)
	require.JSONEq(t, `{"studyServiceJobId":"job-1","studyInstanceUID":"1.2.3","modelName":"echo-sr-measurements","modelVersion":"1.0.0","status":"completed","completedAt":null,"result":{"summary":{}}}`,
		string(decodeViewerData(t, recorder)))

	for code, status := range map[string]int{
		apiError.MissingRecord:                        http.StatusNotFound,
		apiError.ForbiddenAccess:                      http.StatusForbidden,
		apiError.InferenceExecutionResultNotAvailable: http.StatusConflict,
		apiError.InferenceExecutionResultInvalid:      http.StatusUnprocessableEntity,
		apiError.InferenceResultServiceUnavailable:    http.StatusServiceUnavailable,
	} {
		service.err = errors.New(code)
		recorder = httptest.NewRecorder()
		controller.GetStudyJobResult(recorder, viewerRequest(http.MethodGet, "/result", "", params))
		require.Equal(t, status, recorder.Code, code)
	}

	recorder = httptest.NewRecorder()
	controller.GetStudyJobResult(recorder, httptest.NewRequest(http.MethodGet, "/result", nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestLatestResultsKeepMeasurementResultsForRestrictedRoles(t *testing.T) {
	artifact := "PanEcho"
	service := &viewerResultsQueryService{latest: serviceTypes.StudyLatestModelResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyLatestModelResult{
		{ModelName: "echo-sr-measurements", StudyServiceJobID: "job-sr"},
		{ModelName: "Unknown-Model", StudyServiceJobID: "job-x"},
		{ModelName: "DeepCORO-JEPA", StudyServiceJobID: "job-j", ArtifactModelID: &artifact},
	}}}
	controller := InferenceQueryController{InferenceQueryServiceInterface: service}
	request := viewerRequest(http.MethodGet, "/latest-results", "", map[string]string{"studyInstanceUID": "1.2.3"})
	request = request.WithContext(accessctx.WithAccess(request.Context(), restrictedAccess("PanEcho")))
	recorder := httptest.NewRecorder()

	controller.GetStudyLatestModelResults(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var data serviceTypes.StudyLatestModelResults
	require.NoError(t, json.Unmarshal(decodeViewerData(t, recorder), &data))
	names := []string{}
	for _, result := range data.Results {
		names = append(names, result.ModelName)
	}
	require.Equal(t, []string{"echo-sr-measurements", "DeepCORO-JEPA"}, names)
}
