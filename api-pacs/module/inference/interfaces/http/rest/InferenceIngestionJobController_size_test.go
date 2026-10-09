package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type importCSVServiceStub struct {
	application.InferenceCommandServiceInterface
	called    bool
	createCtx context.Context
	importCtx context.Context
}

func (stub *importCSVServiceStub) CreateInferenceIngestionJob(ctx context.Context, _ serviceTypes.CreateInferenceIngestionJob) error {
	stub.createCtx = ctx
	return nil
}

func (stub *importCSVServiceStub) ImportInferenceIngestionJobs(ctx context.Context, _ []serviceTypes.CreateInferenceIngestionJob) error {
	stub.called = true
	stub.importCtx = ctx
	return nil
}

func cancelledTenantRequest(request *http.Request) *http.Request {
	ctx, cancel := context.WithCancel(context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-test"))
	cancel()
	return request.WithContext(ctx)
}

func TestCreateInferenceIngestionJobPropagatesRequestCancellation(t *testing.T) {
	body := `{"dicomModality":"US","containerId":"container-1","modelId":"model-1","modelName":"Model","modelVersion":"1.0.0","modalities":["US"],"stabilityMinutes":1}`
	request := httptest.NewRequest(http.MethodPost, "/v1/inference/ingestion/jobs", strings.NewReader(body))
	request = cancelledTenantRequest(request)
	recorder := httptest.NewRecorder()
	serviceStub := &importCSVServiceStub{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: serviceStub}

	controller.CreateInferenceIngestionJob(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if serviceStub.createCtx == nil || !errors.Is(serviceStub.createCtx.Err(), context.Canceled) {
		t.Fatalf("request cancellation was not propagated: %v", serviceStub.createCtx)
	}
}

func TestImportInferenceIngestionJobsPropagatesRequestCancellation(t *testing.T) {
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="jobs.csv"`)
	header.Set("Content-Type", "text/csv")
	part, err := multipartWriter.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("dicom_modality,container_id,model_id,model_name,model_version,modalities,stability_minutes\nUS,container-1,model-1,Model,1.0.0,US,1\n")); err != nil {
		t.Fatal(err)
	}
	if err = multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/inference/ingestion/jobs/import", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request = cancelledTenantRequest(request)
	recorder := httptest.NewRecorder()
	serviceStub := &importCSVServiceStub{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: serviceStub}

	controller.ImportInferenceIngestionJobsCSVFile(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if serviceStub.importCtx == nil || !errors.Is(serviceStub.importCtx.Err(), context.Canceled) {
		t.Fatalf("request cancellation was not propagated: %v", serviceStub.importCtx)
	}
}

func TestImportInferenceIngestionJobsCSVFileReturns413ForOversizedRequest(t *testing.T) {
	originalMax := mediaMaxFileSize
	mediaMaxFileSize = 32
	t.Cleanup(func() { mediaMaxFileSize = originalMax })

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	part, err := multipartWriter.CreateFormFile("file", "jobs.csv")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), int(mediaMaxFileSize+1))); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/inference/ingestion/jobs/import", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-test"))
	recorder := httptest.NewRecorder()
	serviceStub := &importCSVServiceStub{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: serviceStub}

	controller.ImportInferenceIngestionJobsCSVFile(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ErrorCode != "REQUEST_BODY_TOO_LARGE" {
		t.Fatalf("expected stable oversized error code, got %q", response.ErrorCode)
	}
	if serviceStub.called {
		t.Fatal("service must not be called for an oversized request")
	}
}
