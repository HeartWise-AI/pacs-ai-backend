package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type runLabelsQueryService struct {
	application.InferenceQueryServiceInterface
	read    serviceTypes.GetRunLabels
	change  serviceTypes.ChangeRunLabel
	added   bool
	labels  serviceTypes.RunLabels
	catalog serviceTypes.RunLabelCatalog
	err     error
}

func (service *runLabelsQueryService) GetStudyRunLabels(_ context.Context, data serviceTypes.GetRunLabels) (serviceTypes.RunLabels, error) {
	service.read = data
	return service.labels, service.err
}

func (service *runLabelsQueryService) AddStudyRunLabel(_ context.Context, data serviceTypes.ChangeRunLabel) (serviceTypes.RunLabels, bool, error) {
	service.change = data
	return service.labels, service.added, service.err
}

func (service *runLabelsQueryService) RemoveStudyRunLabel(_ context.Context, data serviceTypes.ChangeRunLabel) (serviceTypes.RunLabels, error) {
	service.change = data
	return service.labels, service.err
}

func (service *runLabelsQueryService) GetRunLabelCatalog(context.Context, string) (serviceTypes.RunLabelCatalog, error) {
	return service.catalog, service.err
}

func labelRequest(method, target, body string, access accessEntity.Access) *http.Request {
	request := viewerRequest(method, target, body, map[string]string{"studyInstanceUID": "1.2.3", "studyServiceJobId": "job-1"})
	return request.WithContext(accessctx.WithAccess(request.Context(), access))
}

func TestRunLabelHandlers(t *testing.T) {
	admin := accessEntity.ForRole(iamEntity.AdminRole, nil)
	service := &runLabelsQueryService{added: true, labels: serviceTypes.RunLabels{StudyServiceJobID: "job-1", Labels: []serviceTypes.RunLabel{{Label: "post PCI"}}}}
	controller := &InferenceQueryController{InferenceQueryServiceInterface: service}

	recorder := httptest.NewRecorder()
	controller.AddStudyRunLabel(recorder, labelRequest(http.MethodPost, "/labels", `{"label":"post PCI"}`, admin))
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "post PCI", service.change.Label)
	require.Equal(t, "user-a", service.change.UserID)
	require.Equal(t, "job-1", service.change.StudyServiceJobID)
	require.Nil(t, service.change.AllowModel, "admins see every model")

	service.added = false
	recorder = httptest.NewRecorder()
	controller.AddStudyRunLabel(recorder, labelRequest(http.MethodPost, "/labels", `{"label":"post PCI"}`, admin))
	require.Equal(t, http.StatusOK, recorder.Code)

	recorder = httptest.NewRecorder()
	controller.AddStudyRunLabel(recorder, labelRequest(http.MethodPost, "/labels", `{"label":"x","other":1}`, admin))
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	recorder = httptest.NewRecorder()
	controller.RemoveStudyRunLabel(recorder, labelRequest(http.MethodDelete, "/labels?label=post+PCI", "", admin))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "post PCI", service.change.Label)

	recorder = httptest.NewRecorder()
	controller.GetStudyRunLabels(recorder, labelRequest(http.MethodGet, "/labels", "", restrictedAccess("PanEcho")))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, service.read.AllowModel, "a restricted role is checked against the run's model")
	var body struct {
		Data serviceTypes.RunLabels `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "post PCI", body.Data.Labels[0].Label)

	for code, status := range map[string]int{
		apiError.ForbiddenAccess:                   http.StatusForbidden,
		apiError.MissingRecord:                     http.StatusNotFound,
		apiError.InvalidPayload:                    http.StatusBadRequest,
		apiError.InferenceResultServiceUnavailable: http.StatusServiceUnavailable,
	} {
		service.err = errors.New(code)
		recorder = httptest.NewRecorder()
		controller.AddStudyRunLabel(recorder, labelRequest(http.MethodPost, "/labels", `{"label":"x"}`, admin))
		require.Equal(t, status, recorder.Code, code)
	}

	service.err = nil
	service.catalog = serviceTypes.RunLabelCatalog{Labels: []serviceTypes.RunLabelCatalogEntry{{Label: "DIAGNOSTIC", RunCount: 4}}}
	recorder = httptest.NewRecorder()
	controller.GetRunLabelCatalog(recorder, labelRequest(http.MethodGet, "/worklist/run-labels", "", admin))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"runCount":4`)
}

func TestLatestResultsPassesTheLabelQuery(t *testing.T) {
	service := &viewerResultsQueryService{}
	controller := &InferenceQueryController{InferenceQueryServiceInterface: service}
	request := labelRequest(http.MethodGet, "/latest-results?label=POST+PCI&modality=angiogram", "", accessEntity.ForRole(iamEntity.AdminRole, nil))
	recorder := httptest.NewRecorder()

	controller.GetStudyLatestModelResults(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "POST PCI", service.latestInput.Label)
	require.Equal(t, "angiogram", service.latestInput.Modality)
}
