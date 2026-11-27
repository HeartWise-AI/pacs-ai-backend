package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	accessEntity "api-pacs/module/access/domain/entity"
	iamEntity "api-pacs/module/iam/domain/entity"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

func chatFeedbackRouter(role string) http.Handler {
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
		r.Route("/orchestrator", func(r chi.Router) {
			mountChatFeedbackRoutes(r, middleware.RequirePermission(accessEntity.PermFeedbackVote), chatFeedbackHandlers{submit: respond, remove: respond})
		})
		r.Route("/chat", func(r chi.Router) {
			mountChatFeedbackAdminRoutes(r, middleware.RequirePermission(accessEntity.PermFeedbackView), chatFeedbackAdminHandlers{list: respond, counts: respond, get: respond})
		})
	})
	return router
}

func TestChatFeedbackVotesAreOpenToRegularUsers(t *testing.T) {
	path := "/orchestrator/threads/thread-1/messages/answer-1/feedback"
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run("unauthenticated "+method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			chatFeedbackRouter(iamEntity.UserRole).ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
		t.Run("regular user "+method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			chatFeedbackRouter(iamEntity.UserRole).ServeHTTP(recorder, authenticatedWorklistRequest(method, path))
			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}

func TestChatFeedbackAdminRoutesRequireFeedbackView(t *testing.T) {
	for _, path := range []string{"/chat/feedback/list?rating=down", "/chat/feedback/counts?days=30", "/chat/feedback/2f1c7a9e-0000-4000-8000-000000000001"} {
		t.Run("unauthenticated "+path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			chatFeedbackRouter(iamEntity.AdminRole).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
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
			t.Run(testCase.role+" "+path, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				chatFeedbackRouter(testCase.role).ServeHTTP(recorder, authenticatedWorklistRequest(http.MethodGet, path))
				require.Equal(t, testCase.want, recorder.Code)
			})
		}
	}
}
