package rest

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type ingestionJobServiceCapture struct {
	application.InferenceCommandServiceInterface
	created    []serviceTypes.CreateInferenceIngestionJob
	imported   []serviceTypes.CreateInferenceIngestionJob
	updated    []serviceTypes.UpdateInferenceIngestionJob
	upgraded   []serviceTypes.UpgradeInferenceIngestionJob
	upgradeErr error
}

func (capture *ingestionJobServiceCapture) CreateInferenceIngestionJob(_ context.Context, data serviceTypes.CreateInferenceIngestionJob) error {
	capture.created = append(capture.created, data)
	return nil
}

func (capture *ingestionJobServiceCapture) ImportInferenceIngestionJobs(_ context.Context, data []serviceTypes.CreateInferenceIngestionJob) error {
	capture.imported = append(capture.imported, data...)
	return nil
}

func (capture *ingestionJobServiceCapture) UpdateInferenceIngestionJob(_ context.Context, data serviceTypes.UpdateInferenceIngestionJob) error {
	capture.updated = append(capture.updated, data)
	return nil
}

func (capture *ingestionJobServiceCapture) UpgradeInferenceIngestionJob(_ context.Context, data serviceTypes.UpgradeInferenceIngestionJob) error {
	capture.upgraded = append(capture.upgraded, data)
	return capture.upgradeErr
}

func authenticatedIngestionRequest(request *http.Request) *http.Request {
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-creator")
	ctx = context.WithValue(ctx, iamTypes.RoleCtx, "ADMIN")
	return request.WithContext(ctx)
}

const ingestionJobBody = `"dicomModality":"US","containerId":"c","modelId":"m","modelName":"n","modelVersion":"1","modalities":["US"],"stabilityMinutes":10`

func TestCreateInferenceIngestionJobRecordsCreatorAndOptionalSwitch(t *testing.T) {
	capture := &ingestionJobServiceCapture{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: capture}
	for _, body := range []string{
		`{` + ingestionJobBody + `}`,
		`{` + ingestionJobBody + `,"notificationsEnabled":false}`,
	} {
		recorder := httptest.NewRecorder()
		request := authenticatedIngestionRequest(httptest.NewRequest(http.MethodPost, "/v1/inference/ingestion/jobs", strings.NewReader(body)))
		controller.CreateInferenceIngestionJob(recorder, request)
		require.Less(t, recorder.Code, 300, recorder.Body.String())
	}
	require.Len(t, capture.created, 2)
	require.Equal(t, "user-creator", capture.created[0].CreatedByUserID)
	require.Nil(t, capture.created[0].NotificationsEnabled)
	require.NotNil(t, capture.created[1].NotificationsEnabled)
	require.False(t, *capture.created[1].NotificationsEnabled)
}

func TestUpdateInferenceIngestionJobLeavesSwitchUnchangedWhenAbsent(t *testing.T) {
	capture := &ingestionJobServiceCapture{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: capture}
	for _, body := range []string{
		`{"modalities":["US"],"stabilityMinutes":10}`,
		`{"modalities":["US"],"stabilityMinutes":10,"notificationsEnabled":false}`,
		`{"modalities":["US"],"stabilityMinutes":10,"notificationsEnabled":true}`,
	} {
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("ID", "job-1")
		request := httptest.NewRequest(http.MethodPut, "/v1/inference/ingestion/jobs/job-1", strings.NewReader(body))
		request = authenticatedIngestionRequest(request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)))
		recorder := httptest.NewRecorder()
		controller.UpdateInferenceIngestionJob(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}
	require.Len(t, capture.updated, 3)
	require.Nil(t, capture.updated[0].NotificationsEnabled)
	require.False(t, *capture.updated[1].NotificationsEnabled)
	require.True(t, *capture.updated[2].NotificationsEnabled)
}

func TestUpgradeInferenceIngestionJobUsesAuthenticatedTenantAndMapsConflict(t *testing.T) {
	capture := &ingestionJobServiceCapture{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: capture}
	requestFor := func() *http.Request {
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("ID", "job-1")
		request := httptest.NewRequest(http.MethodPost, "/v1/inference/ingestion/job/job-1/upgrade", strings.NewReader(`{"expectedModelVersion":"1.0.0"}`))
		return authenticatedIngestionRequest(request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)))
	}

	recorder := httptest.NewRecorder()
	controller.UpgradeInferenceIngestionJob(recorder, requestFor())
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, []serviceTypes.UpgradeInferenceIngestionJob{{
		ID: "job-1", TenantID: "tenant-a", ExpectedModelVersion: "1.0.0",
	}}, capture.upgraded)

	capture.upgradeErr = errors.New(apiError.InferenceIngestionJobUpgradeConflict)
	recorder = httptest.NewRecorder()
	controller.UpgradeInferenceIngestionJob(recorder, requestFor())
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
}

func csvImportRequest(t *testing.T, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="jobs.csv"`)
	header.Set("Content-Type", "text/csv")
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/v1/inference/ingestion/jobs/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return authenticatedIngestionRequest(request)
}

func TestImportInferenceIngestionJobsCSVKeepsExistingFilesValid(t *testing.T) {
	capture := &ingestionJobServiceCapture{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: capture}
	recorder := httptest.NewRecorder()
	controller.ImportInferenceIngestionJobsCSVFile(recorder, csvImportRequest(t,
		"dicom_modality,container_id,model_id,model_name,model_version,modalities,stability_minutes\n"+
			"US,c,m,n,1,US,10\n"))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.Len(t, capture.imported, 1)
	require.Equal(t, "user-creator", capture.imported[0].CreatedByUserID)
	require.Nil(t, capture.imported[0].NotificationsEnabled)
}

func TestImportInferenceIngestionJobsCSVReadsOptionalNotificationsColumn(t *testing.T) {
	capture := &ingestionJobServiceCapture{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: capture}
	recorder := httptest.NewRecorder()
	controller.ImportInferenceIngestionJobsCSVFile(recorder, csvImportRequest(t,
		"dicom_modality,container_id,model_id,model_name,model_version,modalities,stability_minutes,notifications_enabled\n"+
			"US,c,m,n,1,US,10,false\n"+
			"US,c,m,n,1,US,10,\n"+
			"US,c,m,n,1,US,10,yes\n"))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.Len(t, capture.imported, 3)
	require.False(t, *capture.imported[0].NotificationsEnabled)
	require.Nil(t, capture.imported[1].NotificationsEnabled)
	require.True(t, *capture.imported[2].NotificationsEnabled)

	recorder = httptest.NewRecorder()
	controller.ImportInferenceIngestionJobsCSVFile(recorder, csvImportRequest(t,
		"dicom_modality,container_id,model_id,model_name,model_version,modalities,stability_minutes,notifications_enabled\n"+
			"US,c,m,n,1,US,10,maybe\n"))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
