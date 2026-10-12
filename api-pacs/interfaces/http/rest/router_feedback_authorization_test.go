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
