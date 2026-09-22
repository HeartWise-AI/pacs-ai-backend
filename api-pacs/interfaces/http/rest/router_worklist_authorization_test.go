package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	iamContext "api-pacs/interfaces/http/rest/middlewares/iam/types"
	iamEntity "api-pacs/module/iam/domain/entity"
)

func TestWorklistRoutesAllowReadsAndProtectReprocessing(t *testing.T) {
	router := chi.NewRouter()
	respond := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}
	ownerOrAdmin := (&iamMiddleware.IAMMiddleware{}).RBACOwnerOrAdminGuard

	mountInferenceWorklistRoutes(router, ownerOrAdmin, inferenceWorklistHandlers{
		getStatuses:        respond,
		streamEvents:       respond,
		getRunHistory:      respond,
		getRunDetail:       respond,
		getExecutionResult: respond,
		reprocessStudy:     respond,
	})

	for _, path := range []string{
		"/worklist/status",
		"/worklist/events",
		"/worklist/studies/1.2.3/runs",
		"/processing/runs/run-1",
		"/processing/runs/run-1/executions/execution-1/result",
	} {
		t.Run("regular user reads "+path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}

	t.Run("regular user cannot reprocess", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/worklist/studies/1.2.3/reprocess", nil)
		request = request.WithContext(context.WithValue(request.Context(), iamContext.RoleCtx, iamEntity.UserRole))
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	for name, role := range map[string]string{
		"admin": iamEntity.AdminRole,
		"owner": iamEntity.OwnerRole,
	} {
		t.Run(name+" can reprocess", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/worklist/studies/1.2.3/reprocess", nil)
			request = request.WithContext(context.WithValue(request.Context(), iamContext.RoleCtx, role))
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}
}
