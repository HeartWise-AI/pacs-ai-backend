package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	iamEntity "api-pacs/module/iam/domain/entity"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

func TestFeedbackRoutesAllowAuthenticatedRegularUsers(t *testing.T) {
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

	router.Group(func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		r.Use(middleware.PolicyAcceptanceGuard)
		r.Route("/model", func(r chi.Router) {
			mountInferenceFeedbackRoutes(r, inferenceFeedbackHandlers{
				get:    respond,
				update: respond,
				remove: respond,
			})
		})
	})

	t.Run("unauthenticated feedback read is rejected", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/model/model-1/feedback", nil)
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/model/model-1/feedback"},
		{method: http.MethodPut, path: "/model/feedback/update"},
		{method: http.MethodDelete, path: "/model/model-1/feedback/remove"},
	} {
		t.Run("regular user "+request.method+" "+request.path, func(t *testing.T) {
			httpRequest := authenticatedWorklistRequest(request.method, request.path)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, httpRequest)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func feedbackListRouter(role string) http.Handler {
	router := chi.NewRouter()
	respond := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: role},
		UserQueryServiceInterface: &worklistPolicyQueryService{
			status: userServiceTypes.PolicyStatus{EnforcementActive: true},
		},
	}

	router.Group(func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		r.Use(middleware.PolicyAcceptanceGuard)
		r.Route("/model", func(r chi.Router) {
			mountInferenceFeedbackRoutes(r, inferenceFeedbackHandlers{
				get:    respond,
				update: respond,
				remove: respond,
			})
			mountInferenceFeedbackAdminRoutes(r, middleware.RBACOwnerOrAdminGuard, respond)
		})
	})
	return router
}

func TestFeedbackListRouteRequiresOwnerOrAdmin(t *testing.T) {
	t.Run("unauthenticated list is rejected", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		feedbackListRouter(iamEntity.AdminRole).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/model/feedback/list", nil))
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	for _, testCase := range []struct {
		role string
		want int
	}{
		{role: iamEntity.UserRole, want: http.StatusForbidden},
		{role: iamEntity.AdminRole, want: http.StatusNoContent},
		{role: iamEntity.OwnerRole, want: http.StatusNoContent},
	} {
		t.Run("role "+testCase.role, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			feedbackListRouter(testCase.role).ServeHTTP(recorder, authenticatedWorklistRequest(http.MethodGet, "/model/feedback/list"))
			require.Equal(t, testCase.want, recorder.Code)
		})
	}

	t.Run("regular user keeps reading their own feedback", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		feedbackListRouter(iamEntity.UserRole).ServeHTTP(recorder, authenticatedWorklistRequest(http.MethodGet, "/model/model-1/feedback"))
		require.Equal(t, http.StatusNoContent, recorder.Code)
	})
}
