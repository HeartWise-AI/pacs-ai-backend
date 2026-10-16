package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type deploymentControllerService struct {
	application.InferenceCommandServiceInterface
	startInput serviceTypes.DeployInferenceModelVersion
	getInput   serviceTypes.GetInferenceModelDeploymentOperation
	operation  entity.InferenceModelDeploymentOperation
	err        error
}

func (service *deploymentControllerService) DeployInferenceModelVersion(
	_ context.Context,
	input serviceTypes.DeployInferenceModelVersion,
) (serviceTypes.DeployInferenceModelVersionResult, error) {
	service.startInput = input
	return serviceTypes.DeployInferenceModelVersionResult{Operation: service.operation}, service.err
}

func (service *deploymentControllerService) GetInferenceModelDeploymentOperation(
	_ context.Context,
	input serviceTypes.GetInferenceModelDeploymentOperation,
) (entity.InferenceModelDeploymentOperation, error) {
	service.getInput = input
	return service.operation, service.err
}

func deploymentControllerRequest(method, path string, routeParams map[string]string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	routeContext := chi.NewRouteContext()
	for key, value := range routeParams {
		routeContext.URLParams.Add(key, value)
	}
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)
	ctx = context.WithValue(ctx, iamTypes.TenantIDCtx, "tenant-1")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-1")
	return request.WithContext(ctx)
}

func TestDeployInferenceModelVersionReturnsAcceptedOperation(t *testing.T) {
	service := &deploymentControllerService{operation: entity.InferenceModelDeploymentOperation{
		ID: "operation-1", Status: entity.InferenceModelDeploymentOperationPending,
	}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	request := deploymentControllerRequest(http.MethodPost, "/models/registration-1/versions/catalog-1/deploy", map[string]string{
		"ID": "registration-1", "versionID": "catalog-1",
	})
	recorder := httptest.NewRecorder()

	controller.DeployInferenceModelVersion(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.startInput != (serviceTypes.DeployInferenceModelVersion{
		TenantID: "tenant-1", ModelID: "registration-1", VersionID: "catalog-1", ActorUserID: "user-1",
	}) {
		t.Fatalf("start input = %+v", service.startInput)
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			DeploymentID string                                         `json:"deploymentId"`
			Status       entity.InferenceModelDeploymentOperationStatus `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Data.DeploymentID != "operation-1" ||
		response.Data.Status != entity.InferenceModelDeploymentOperationPending {
		t.Fatalf("response = %+v", response)
	}
}

func TestGetInferenceModelDeploymentOperationUsesAuthenticatedScope(t *testing.T) {
	service := &deploymentControllerService{operation: entity.InferenceModelDeploymentOperation{ID: "operation-1"}}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	request := deploymentControllerRequest(http.MethodGet, "/models/registration-1/deployments/operation-1", map[string]string{
		"ID": "registration-1", "deploymentID": "operation-1",
	})
	recorder := httptest.NewRecorder()

	controller.GetInferenceModelDeploymentOperation(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.getInput != (serviceTypes.GetInferenceModelDeploymentOperation{
		TenantID: "tenant-1", ModelID: "registration-1", DeploymentID: "operation-1",
	}) {
		t.Fatalf("get input = %+v", service.getInput)
	}
}

func TestDeployInferenceModelVersionRequiresAuthenticatedActor(t *testing.T) {
	service := &deploymentControllerService{}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	request := deploymentControllerRequest(http.MethodPost, "/models/registration-1/versions/catalog-1/deploy", map[string]string{
		"ID": "registration-1", "versionID": "catalog-1",
	})
	request = request.WithContext(context.WithValue(request.Context(), iamTypes.UserIDCtx, ""))
	recorder := httptest.NewRecorder()

	controller.DeployInferenceModelVersion(recorder, request)

	if recorder.Code != http.StatusBadRequest || service.startInput.ModelID != "" {
		t.Fatalf("status = %d input=%+v", recorder.Code, service.startInput)
	}
}
