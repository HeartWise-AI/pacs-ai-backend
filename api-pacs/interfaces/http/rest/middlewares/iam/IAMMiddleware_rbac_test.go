package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/domain/entity"
)

func TestRoleGuardsReturnForbiddenForAuthenticatedUsersWithoutRequiredRole(t *testing.T) {
	middleware := &IAMMiddleware{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for name, guard := range map[string]func(http.Handler) http.Handler{
		"owner":          middleware.RBACOwnerGuard,
		"owner or admin": middleware.RBACOwnerOrAdminGuard,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request = request.WithContext(context.WithValue(request.Context(), types.RoleCtx, entity.UserRole))
			recorder := httptest.NewRecorder()

			guard(next).ServeHTTP(recorder, request)

			require.Equal(t, http.StatusForbidden, recorder.Code)
			var response struct {
				ErrorCode string `json:"errorCode"`
			}
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			require.Equal(t, apiError.ForbiddenAccess, response.ErrorCode)
		})
	}
}
