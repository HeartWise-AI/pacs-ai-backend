package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"api-pacs/module/orchestrator/application"
	serviceTypes "api-pacs/module/orchestrator/infrastructure/service/types"
	httpTypes "api-pacs/module/orchestrator/interfaces/http"
)

func testOrchestratorLimits() orchestratorInputLimits {
	return orchestratorInputLimits{
		MaxRequestBodyBytes:   1024,
		MaxStudies:            2,
		MaxSeriesPerStudy:     2,
		MaxPreviewBase64Bytes: 16,
		MaxMetadataBytes:      32,
		MaxMetadataDepth:      3,
		MaxMetadataEntries:    2,
		MaxMessageBytes:       16,
	}
}

func validOrchestratorDICOMRequest() httpTypes.UploadDicomPayloadRequest {
	return httpTypes.UploadDicomPayloadRequest{Payload: []httpTypes.StudyData{{
		StudyInstanceUID:   "1.2.3",
		SeriesInstanceUIDs: []string{"1.2.3.1", "1.2.3.2"},
		AdditionalMetadata: map[string]interface{}{"view": "A4C"},
	}}}
}

func TestValidateOrchestratorDICOMPayloadBounds(t *testing.T) {
	limits := testOrchestratorLimits()
	require.NoError(t, validateOrchestratorDICOMPayload(validOrchestratorDICOMRequest(), limits))

	tests := map[string]func(*httpTypes.UploadDicomPayloadRequest){
		"too many studies": func(request *httpTypes.UploadDicomPayloadRequest) {
			request.Payload = append(request.Payload, request.Payload[0], request.Payload[0])
		},
		"duplicate study": func(request *httpTypes.UploadDicomPayloadRequest) {
			request.Payload = append(request.Payload, request.Payload[0])
		},
		"too many series": func(request *httpTypes.UploadDicomPayloadRequest) {
			request.Payload[0].SeriesInstanceUIDs = append(request.Payload[0].SeriesInstanceUIDs, "1.2.3.3")
		},
		"duplicate series": func(request *httpTypes.UploadDicomPayloadRequest) {
			request.Payload[0].SeriesInstanceUIDs[1] = request.Payload[0].SeriesInstanceUIDs[0]
		},
		"invalid uid": func(request *httpTypes.UploadDicomPayloadRequest) {
			request.Payload[0].StudyInstanceUID = "not-a-dicom-uid"
		},
		"oversized metadata": func(request *httpTypes.UploadDicomPayloadRequest) {
			request.Payload[0].AdditionalMetadata = map[string]interface{}{"value": strings.Repeat("x", 64)}
		},
		"oversized preview": func(request *httpTypes.UploadDicomPayloadRequest) {
			preview := strings.Repeat("x", limits.MaxPreviewBase64Bytes+1)
			request.Payload[0].PreviewImageBase64 = &preview
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := validOrchestratorDICOMRequest()
			mutate(&request)
			require.Error(t, validateOrchestratorDICOMPayload(request, limits))
		})
	}
}

type orchestratorServiceStub struct {
	application.OrchestratorServiceInterface
	messageCalled bool
}

func (stub *orchestratorServiceStub) CreateMessage(context.Context, serviceTypes.CreateMessageRequest) (serviceTypes.CreateMessageResponse, error) {
	stub.messageCalled = true
	return serviceTypes.CreateMessageResponse{}, nil
}

func TestCreateMessageRejectsOversizedJSONBeforeServiceCall(t *testing.T) {
	t.Setenv("ORCHESTRATOR_MAX_REQUEST_BODY_BYTES", "32")
	body := bytes.NewBufferString(`{"message":"` + strings.Repeat("x", 64) + `"}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/threads/thread-1/chat", body)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("threadID", "thread-1")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	recorder := httptest.NewRecorder()
	serviceStub := &orchestratorServiceStub{}
	controller := NewOrchestratorController(serviceStub)

	controller.CreateMessage(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "REQUEST_BODY_TOO_LARGE", response.ErrorCode)
	require.False(t, serviceStub.messageCalled)
}
