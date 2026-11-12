package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/service"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	"github.com/go-chi/chi/v5"
)

type gpuAdministrationStub struct {
	tenant, actor, model, gpu, expected string
	calls                               int
	err                                 error
}

func (stub *gpuAdministrationStub) Snapshot(_ context.Context, tenant string) (service.GPUAdministrationSnapshot, error) {
	stub.tenant = tenant
	return service.GPUAdministrationSnapshot{}, stub.err
}
func (stub *gpuAdministrationStub) Move(_ context.Context, tenant, actor, model, gpu, expected string) (serviceTypes.DeployInferenceModelVersionResult, error) {
	stub.calls++
	stub.tenant = tenant
	stub.actor = actor
	stub.model = model
	stub.gpu = gpu
	stub.expected = expected
	return serviceTypes.DeployInferenceModelVersionResult{Operation: entity.InferenceModelDeploymentOperation{ID: "deployment", Status: entity.InferenceModelDeploymentOperationPending}}, stub.err
}
func gpuScopedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "authenticated-tenant")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "authenticated-actor")
	route := chi.NewRouteContext()
	route.URLParams.Add("ID", "registration")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	return request.WithContext(ctx)
}
func TestGPUPlacementControllerUsesAuthenticatedScopeAndCAS(t *testing.T) {
	stub := &gpuAdministrationStub{}
	controller := &InferenceCommandController{GPUAdministration: stub}
	recorder := httptest.NewRecorder()
	controller.MoveInferenceModelGPU(recorder, gpuScopedRequest("POST", "/", `{"gpuUuid":"physical-uuid","expectedContainerId":"old-id","tenantId":"attacker","actorUserId":"attacker"}`))
	if recorder.Code != 202 || stub.tenant != "authenticated-tenant" || stub.actor != "authenticated-actor" || stub.model != "registration" || stub.gpu != "physical-uuid" || stub.expected != "old-id" {
		t.Fatalf("incorrect scope/payload: %+v response=%s", stub, recorder.Body.String())
	}
}
func TestGPUControllerBoundsRequestsAndSanitizesFailures(t *testing.T) {
	stub := &gpuAdministrationStub{}
	controller := &InferenceCommandController{GPUAdministration: stub}
	recorder := httptest.NewRecorder()
	controller.MoveInferenceModelGPU(recorder, gpuScopedRequest("POST", "/", `{"gpuUuid":"`+strings.Repeat("a", 5000)+`"}`))
	if recorder.Code != 400 || stub.calls != 0 {
		t.Fatal("oversized request reached service")
	}
	stub.err = errors.New("private-driver-error-secret")
	recorder = httptest.NewRecorder()
	controller.GetGPUs(recorder, gpuScopedRequest("GET", "/", ""))
	if recorder.Code != 500 || strings.Contains(recorder.Body.String(), "private-driver-error-secret") {
		t.Fatal("raw GPU failure escaped")
	}
	if stub.tenant != "authenticated-tenant" {
		t.Fatal("snapshot not tenant scoped")
	}
}
