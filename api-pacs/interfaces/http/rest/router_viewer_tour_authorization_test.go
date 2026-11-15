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

func TestViewerTourRoutesRequireAuthenticatedSession(t *testing.T) {
	router := chi.NewRouter()
	var seenUserID string
	respond := func(w http.ResponseWriter, r *http.Request) {
		seenUserID, _ = r.Context().Value(iamTypes.UserIDCtx).(string)
		w.WriteHeader(http.StatusNoContent)
	}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: iamEntity.UserRole},
		UserQueryServiceInterface: &worklistPolicyQueryService{
			status: userServiceTypes.PolicyStatus{EnforcementActive: true},
		},
	}

	router.Route("/user", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			mountUserViewerTourRoutes(r, userViewerTourHandlers{get: respond, complete: respond, reset: respond})
		})
	})

	routes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/user/viewer-tour"},
		{method: http.MethodPut, path: "/user/viewer-tour/complete"},
		{method: http.MethodPost, path: "/user/viewer-tour/reset"},
	}

	for _, route := range routes {
		t.Run("unauthenticated "+route.method+" "+route.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})

		t.Run("regular user "+route.method+" "+route.path, func(t *testing.T) {
			seenUserID = ""
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, authenticatedWorklistRequest(route.method, route.path))
			require.Equal(t, http.StatusNoContent, recorder.Code)
			require.NotEmpty(t, seenUserID)
		})
	}
}
