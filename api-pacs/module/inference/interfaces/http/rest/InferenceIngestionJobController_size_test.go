package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/application"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type importCSVServiceStub struct {
	application.InferenceCommandServiceInterface
	called bool
}

func (stub *importCSVServiceStub) ImportInferenceIngestionJobs(context.Context, []serviceTypes.CreateInferenceIngestionJob) error {
	stub.called = true
	return nil
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
