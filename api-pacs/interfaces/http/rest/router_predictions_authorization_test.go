package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	iamEntity "api-pacs/module/iam/domain/entity"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

func TestPredictionsRoutesRequireAuthenticationAndPolicyAndCarryTheSessionIdentity(t *testing.T) {
	router := chi.NewRouter()
	var seen struct{ tenant, user, role interface{} }
	respond := func(w http.ResponseWriter, r *http.Request) {
		seen.tenant = r.Context().Value(iamTypes.TenantIDCtx)
		seen.user = r.Context().Value(iamTypes.UserIDCtx)
		seen.role = r.Context().Value(iamTypes.RoleCtx)
		w.WriteHeader(http.StatusNoContent)
	}
	policyService := &worklistPolicyQueryService{status: userServiceTypes.PolicyStatus{EnforcementActive: true}}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: iamEntity.UserRole},
		UserQueryServiceInterface:  policyService,
	}
	mountInferencePredictionsRoutes(router, middleware.TokenSessionAuthGuard, middleware.PolicyAcceptanceGuard,
		inferencePredictionsHandlers{list: respond, history: respond, export: respond})

	paths := []string{
		"/predictions?view=mine",
		"/predictions/export.csv?view=automatic",
		"/predictions/1.2.3/history?model=DeepCORO-JEPA",
	}
	for _, path := range paths {
		t.Run("unauthenticated "+path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run("policy not accepted "+path, func(t *testing.T) {
			policyService.status.AcceptanceRequired = true
			defer func() { policyService.status.AcceptanceRequired = false }()
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, authenticatedWorklistRequest(http.MethodGet, path))
			require.Equal(t, http.StatusPreconditionRequired, recorder.Code)
		})
		t.Run("regular user "+path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			// A tenant in the query string is never used: the session decides.
			router.ServeHTTP(recorder, authenticatedWorklistRequest(http.MethodGet, path+"&tenantId=tenant-b&userId=user-b"))
			require.Equal(t, http.StatusNoContent, recorder.Code)
			require.Equal(t, "tenant-a", seen.tenant)
			require.Equal(t, "user-a", seen.user)
			require.Equal(t, iamEntity.UserRole, seen.role)
		})
	}
}
