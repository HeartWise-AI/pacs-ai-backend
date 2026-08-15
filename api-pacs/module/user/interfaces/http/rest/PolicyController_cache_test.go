package rest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/user/application"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type policyQueryControllerService struct {
	application.UserQueryServiceInterface
}

func (*policyQueryControllerService) GetRegistrationPolicies(_ context.Context, _ string) ([]serviceTypes.PolicyDefinition, error) {
	return []serviceTypes.PolicyDefinition{}, nil
}

func (*policyQueryControllerService) GetPolicyStatus(_ context.Context, _, _ string) (serviceTypes.PolicyStatus, error) {
	return serviceTypes.PolicyStatus{Policies: []serviceTypes.PolicyStatusItem{}}, nil
}

type policyCommandControllerService struct {
	application.UserCommandServiceInterface
}

func (*policyCommandControllerService) AcceptPolicies(_ context.Context, _ serviceTypes.AcceptPolicies) error {
	return nil
}

func TestPolicyResponsesAreNeverCached(t *testing.T) {
	queryController := UserQueryController{UserQueryServiceInterface: &policyQueryControllerService{}}

	registrationRecorder := httptest.NewRecorder()
	queryController.GetRegistrationPolicies(
		registrationRecorder,
		httptest.NewRequest(http.MethodGet, "/v1/user/policies/registration?tenantId=tenant-a", nil),
	)
	require.Equal(t, "no-store", registrationRecorder.Header().Get("Cache-Control"))

	statusRecorder := httptest.NewRecorder()
	statusRequest := httptest.NewRequest(http.MethodGet, "/v1/user/policies/status", nil)
	statusContext := context.WithValue(statusRequest.Context(), iamTypes.TenantIDCtx, "tenant-a")
	statusContext = context.WithValue(statusContext, iamTypes.UserIDCtx, "user-a")
	queryController.GetCurrentUserPolicyStatus(statusRecorder, statusRequest.WithContext(statusContext))
	require.Equal(t, "no-store", statusRecorder.Header().Get("Cache-Control"))

	commandController := UserCommandController{UserCommandServiceInterface: &policyCommandControllerService{}}
	acceptanceRecorder := httptest.NewRecorder()
	acceptanceRequest := httptest.NewRequest(http.MethodPost, "/v1/user/policies/accept", bytes.NewBufferString(`{"acceptances":[{"policyKey":"TERMS_OF_SERVICE","version":"v1"}]}`))
	acceptanceContext := context.WithValue(acceptanceRequest.Context(), iamTypes.TenantIDCtx, "tenant-a")
	acceptanceContext = context.WithValue(acceptanceContext, iamTypes.UserIDCtx, "user-a")
	commandController.AcceptPolicies(acceptanceRecorder, acceptanceRequest.WithContext(acceptanceContext))
	require.Equal(t, "no-store", acceptanceRecorder.Header().Get("Cache-Control"))
}
