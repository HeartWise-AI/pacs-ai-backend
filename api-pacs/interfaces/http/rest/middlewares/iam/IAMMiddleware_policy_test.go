package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	userApplication "api-pacs/module/user/application"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

type policyGuardQueryService struct {
	userApplication.UserQueryServiceInterface
	status   userTypes.PolicyStatus
	err      error
	tenantID string
	userID   string
}

func (service *policyGuardQueryService) GetPolicyStatus(_ context.Context, tenantID, userID string) (userTypes.PolicyStatus, error) {
	service.tenantID = tenantID
	service.userID = userID
	return service.status, service.err
}

func policyGuardRequest() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/v1/inference/quota", nil)
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.UserIDCtx, "user-a")
	return request.WithContext(ctx)
}

func TestPolicyAcceptanceGuardBlocksMissingCurrentAcceptance(t *testing.T) {
	service := &policyGuardQueryService{status: userTypes.PolicyStatus{AcceptanceRequired: true, EnforcementActive: true}}
	middleware := &IAMMiddleware{UserQueryServiceInterface: service}
	nextCalled := false
	recorder := httptest.NewRecorder()

	middleware.PolicyAcceptanceGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })).ServeHTTP(recorder, policyGuardRequest())

	require.Equal(t, http.StatusPreconditionRequired, recorder.Code)
	require.False(t, nextCalled)
	require.Equal(t, "tenant-a", service.tenantID)
	require.Equal(t, "user-a", service.userID)
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.PolicyAcceptanceRequired, response.ErrorCode)
}

func TestPolicyAcceptanceGuardAllowsAcceptedOrGracePeriodUser(t *testing.T) {
	for _, status := range []userTypes.PolicyStatus{
		{AcceptanceRequired: false, EnforcementActive: true},
		{AcceptanceRequired: true, EnforcementActive: false},
	} {
		service := &policyGuardQueryService{status: status}
		middleware := &IAMMiddleware{UserQueryServiceInterface: service}
		recorder := httptest.NewRecorder()
		nextCalled := false

		middleware.PolicyAcceptanceGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })).ServeHTTP(recorder, policyGuardRequest())

		require.True(t, nextCalled)
	}
}

func TestPolicyAcceptanceGuardFailsClosedWhenStatusUnavailable(t *testing.T) {
	service := &policyGuardQueryService{err: errors.New(apiError.FirestoreError)}
	middleware := &IAMMiddleware{UserQueryServiceInterface: service}
	recorder := httptest.NewRecorder()

	middleware.PolicyAcceptanceGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("protected handler must not run") })).ServeHTTP(recorder, policyGuardRequest())

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.FirestoreError, response.ErrorCode)
}
