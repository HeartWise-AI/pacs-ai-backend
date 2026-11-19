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

func TestHandoffExchangeIsPublicAndLinkNeedsASession(t *testing.T) {
	router := chi.NewRouter()
	respond := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: iamEntity.UserRole},
		UserQueryServiceInterface:  &worklistPolicyQueryService{status: userServiceTypes.PolicyStatus{}},
	}
	mountIAMHandoffRoutes(router, middleware.TokenSessionAuthGuard, iamHandoffHandlers{exchange: respond, link: respond})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/handoff/exchange", nil))
	require.Equal(t, http.StatusNoContent, recorder.Code)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/handoff/link", nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedWorklistRequest(http.MethodPost, "/handoff/link"))
	require.Equal(t, http.StatusNoContent, recorder.Code)
}
