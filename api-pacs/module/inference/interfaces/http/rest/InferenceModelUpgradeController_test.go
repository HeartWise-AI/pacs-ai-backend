package rest

import (
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
	"api-pacs/module/inference/infrastructure/modelupgrade"
)

type modelUpgradeControllerService struct {
	application.InferenceModelUpgradeServiceInterface
	startRequest application.StartInferenceModelUpgrade
	upgrade      entity.InferenceModelUpgrade
	err          error
}

type modelDeletionControllerService struct {
	application.InferenceCommandServiceInterface
	err error
}

func (service *modelDeletionControllerService) RemoveInferenceModel(context.Context, string) error {
	return service.err
}

func (service *modelUpgradeControllerService) StartInferenceModelUpgrade(_ context.Context, request application.StartInferenceModelUpgrade) (entity.InferenceModelUpgrade, error) {
	service.startRequest = request
	return service.upgrade, service.err
}

func (service *modelUpgradeControllerService) GetInferenceModelUpgrade(context.Context, string, string, string) (entity.InferenceModelUpgrade, error) {
	return service.upgrade, service.err
}

func (service *modelUpgradeControllerService) CancelInferenceModelUpgrade(context.Context, string, string, string) (entity.InferenceModelUpgrade, error) {
	return service.upgrade, service.err
}

func modelUpgradeRequest(method, path, body, modelID, upgradeID string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-1")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-1")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("modelID", modelID)
	if upgradeID != "" {
		routeContext.URLParams.Add("upgradeID", upgradeID)
	}
	return request.WithContext(context.WithValue(ctx, chi.RouteCtxKey, routeContext))
}

func TestStartInferenceModelUpgradeForwardsAuthenticatedIdentity(t *testing.T) {
	service := &modelUpgradeControllerService{upgrade: entity.InferenceModelUpgrade{ID: "upgrade-1", State: entity.InferenceModelUpgradeQueued}}
	controller := InferenceCommandController{InferenceModelUpgradeServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.StartInferenceModelUpgrade(recorder, modelUpgradeRequest(
		http.MethodPost, "/v1/inference/model/model-1/upgrade",
		`{"imageReference":"heartwisehub/model:2.0.0","expectedDigest":"sha256:abc","drainTimeoutSeconds":30}`,
		"model-1", "",
	))

	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "tenant-1", service.startRequest.TenantID)
	require.Equal(t, "user-1", service.startRequest.ActorUserID)
	require.Equal(t, "model-1", service.startRequest.ModelID)
	require.Equal(t, "heartwisehub/model:2.0.0", service.startRequest.ImageReference)
	require.Equal(t, 30, service.startRequest.DrainTimeoutSeconds)
}

func TestStartInferenceModelUpgradeRejectsUnknownFields(t *testing.T) {
	service := &modelUpgradeControllerService{}
	controller := InferenceCommandController{InferenceModelUpgradeServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.StartInferenceModelUpgrade(recorder, modelUpgradeRequest(
		http.MethodPost, "/v1/inference/model/model-1/upgrade",
		`{"imageReference":"heartwisehub/model:2.0.0","registryToken":"must-not-be-accepted"}`,
		"model-1", "",
	))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, service.startRequest.ImageReference)
}

func TestStartInferenceModelUpgradeMapsConcurrentAttemptToConflict(t *testing.T) {
	service := &modelUpgradeControllerService{err: &modelupgrade.HTTPError{
		Status: http.StatusConflict, Code: "INFERENCE_UPGRADE_CONFLICT",
		Message: "A model upgrade is already active.", Cause: modelupgrade.ErrConflict,
	}}
	controller := InferenceCommandController{InferenceModelUpgradeServiceInterface: service}
	recorder := httptest.NewRecorder()
	controller.StartInferenceModelUpgrade(recorder, modelUpgradeRequest(
		http.MethodPost, "/v1/inference/model/model-1/upgrade",
		`{"imageReference":"heartwisehub/model:2.0.0"}`, "model-1", "",
	))

	require.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, "INFERENCE_UPGRADE_CONFLICT", response.ErrorCode)
}

func TestRemoveInferenceModelMapsActiveUpgradeToConflict(t *testing.T) {
	service := &modelDeletionControllerService{err: errors.New(apiError.InferenceUpgradeConflict)}
	controller := InferenceCommandController{InferenceCommandServiceInterface: service}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/v1/inference/model/model-1", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("ID", "model-1")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))

	controller.RemoveInferenceModel(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	var response struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Success)
	require.Equal(t, apiError.InferenceUpgradeConflict, response.ErrorCode)
}

func TestModelUpgradeEndpointIsUnavailableWithoutManager(t *testing.T) {
	controller := InferenceCommandController{}
	recorder := httptest.NewRecorder()
	controller.GetInferenceModelUpgrade(recorder, modelUpgradeRequest(
		http.MethodGet, "/v1/inference/model/model-1/upgrade/upgrade-1", "", "model-1", "upgrade-1",
	))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
