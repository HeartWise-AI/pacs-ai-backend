package rest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamServiceTypes "api-pacs/module/iam/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

type worklistAuthQueryService struct {
	iamApplication.IAMQueryServiceInterface
	role string
}

func (service *worklistAuthQueryService) GetSessionToken(_ context.Context, token string) (iamEntity.TokenSession, error) {
	if token != "session" {
		return iamEntity.TokenSession{}, errors.New("invalid session")
	}
	return iamEntity.TokenSession{TenantID: "tenant-a", UserID: "user-a", Role: service.role}, nil
}

func (*worklistAuthQueryService) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

type worklistAuthCommandService struct {
	iamApplication.IAMCommandServiceInterface
}

func (*worklistAuthCommandService) SetTokenSession(context.Context, iamServiceTypes.SetTokenSession) error {
	return nil
}

type worklistPolicyQueryService struct {
	userApplication.UserQueryServiceInterface
	status userServiceTypes.PolicyStatus
}

func (service *worklistPolicyQueryService) GetPolicyStatus(context.Context, string, string) (userServiceTypes.PolicyStatus, error) {
	return service.status, nil
}

func TestWorklistRoutesAllowReadsAndProtectReprocessing(t *testing.T) {
	router := chi.NewRouter()
	respond := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	policyService := &worklistPolicyQueryService{
		status: userServiceTypes.PolicyStatus{EnforcementActive: true},
	}
	authService := &worklistAuthQueryService{role: iamEntity.UserRole}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   authService,
		UserQueryServiceInterface:  policyService,
	}

	mountInferenceWorklistRoutes(
		router,
		middleware.TokenSessionAuthGuard,
		middleware.PolicyAcceptanceGuard,
		middleware.RBACOwnerOrAdminGuard,
		inferenceWorklistHandlers{
			getStatuses:        respond,
			streamEvents:       respond,
			getRunHistory:      respond,
			getRunDetail:       respond,
			getExecutionResult: respond,
			reprocessStudy:     respond,
		},
	)

	t.Run("unauthenticated read is rejected", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/worklist/status", nil)
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	t.Run("unaccepted policy blocks read", func(t *testing.T) {
		policyService.status.AcceptanceRequired = true
		defer func() { policyService.status.AcceptanceRequired = false }()
		request := authenticatedWorklistRequest(http.MethodGet, "/worklist/status")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusPreconditionRequired, recorder.Code)
	})

	for _, path := range []string{
		"/worklist/status",
		"/worklist/events",
		"/worklist/studies/1.2.3/runs",
		"/processing/runs/run-1",
		"/processing/runs/run-1/executions/execution-1/result",
	} {
		t.Run("regular user reads "+path, func(t *testing.T) {
			request := authenticatedWorklistRequest(http.MethodGet, path)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}

	t.Run("regular user cannot reprocess", func(t *testing.T) {
		request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/1.2.3/reprocess")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	for name, role := range map[string]string{
		"admin": iamEntity.AdminRole,
		"owner": iamEntity.OwnerRole,
	} {
		t.Run(name+" can reprocess", func(t *testing.T) {
			authService.role = role
			defer func() { authService.role = iamEntity.UserRole }()
			request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/1.2.3/reprocess")
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func authenticatedWorklistRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer session")
	return request
}
