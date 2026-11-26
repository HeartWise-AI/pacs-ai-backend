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
	accessEntity "api-pacs/module/access/domain/entity"
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
		middleware.RequirePermission(accessEntity.PermModelsUse),
		middleware.RequirePermission(accessEntity.PermProcessingManage),
		middleware.RequireAllModels,
		inferenceWorklistHandlers{
			getStatuses:        respond,
			streamEvents:       respond,
			getRunHistory:      respond,
			getRunDetail:       respond,
			getExecutionResult: respond,
			reprocessStudy:     respond,
			reprocessStudies:   respond,
			getLatestResults:   respond,
			getExecutionReport: respond,
			getStudyJobReport:  respond,
			runStudyModel:      respond,
			getStudyMedia:      respond,
			getStudyMediaFile:  respond,
			cancelRun:          respond,
			getRunLabels:       respond,
			addRunLabel:        respond,
			removeRunLabel:     respond,
			getRunLabelCatalog: respond,
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
		"/worklist/studies/1.2.3/latest-results",
		"/processing/runs/run-1/executions/execution-1/report",
		"/worklist/studies/1.2.3/jobs/job-1/report",
		"/worklist/studies/1.2.3/media",
		"/media/media-1",
		"/worklist/run-labels",
		"/worklist/studies/1.2.3/jobs/job-1/labels",
	} {
		t.Run("regular user reads "+path, func(t *testing.T) {
			request := authenticatedWorklistRequest(http.MethodGet, path)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})
	}

	for _, path := range []string{"/worklist/studies/1.2.3/media", "/media/media-1"} {
		t.Run("media requires authentication "+path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}

	t.Run("regular user can run one model from the viewer", func(t *testing.T) {
		request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/1.2.3/models/registration-1/run")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("viewer run requires authentication", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/worklist/studies/1.2.3/models/registration-1/run", nil)
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	t.Run("viewer run requires accepted policy", func(t *testing.T) {
		policyService.status.AcceptanceRequired = true
		defer func() { policyService.status.AcceptanceRequired = false }()
		request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/1.2.3/models/registration-1/run")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusPreconditionRequired, recorder.Code)
	})

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run("regular user can change run labels "+method, func(t *testing.T) {
			request := authenticatedWorklistRequest(method, "/worklist/studies/1.2.3/jobs/job-1/labels")
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusNoContent, recorder.Code)
		})

		t.Run("changing run labels needs models.use "+method, func(t *testing.T) {
			authService.role = "VIEWER"
			defer func() { authService.role = iamEntity.UserRole }()
			request := authenticatedWorklistRequest(method, "/worklist/studies/1.2.3/jobs/job-1/labels")
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusForbidden, recorder.Code)
		})

		t.Run("changing run labels requires authentication "+method, func(t *testing.T) {
			request := httptest.NewRequest(method, "/worklist/studies/1.2.3/jobs/job-1/labels", nil)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}

	t.Run("regular user can cancel the waiting models of a run", func(t *testing.T) {
		request := authenticatedWorklistRequest(http.MethodPost, "/processing/runs/run-1/cancel")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("run cancellation requires authentication", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/processing/runs/run-1/cancel", nil)
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})

	t.Run("run cancellation requires accepted policy", func(t *testing.T) {
		policyService.status.AcceptanceRequired = true
		defer func() { policyService.status.AcceptanceRequired = false }()
		request := authenticatedWorklistRequest(http.MethodPost, "/processing/runs/run-1/cancel")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusPreconditionRequired, recorder.Code)
	})

	t.Run("run cancellation is not readable", func(t *testing.T) {
		request := authenticatedWorklistRequest(http.MethodGet, "/processing/runs/run-1/cancel")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	})

	t.Run("regular user cannot reprocess", func(t *testing.T) {
		request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/1.2.3/reprocess")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
	})

	t.Run("regular user cannot bulk reprocess", func(t *testing.T) {
		request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/reprocess-batch")
		recorder := httptest.NewRecorder()

		router.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
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

		t.Run(name+" can bulk reprocess", func(t *testing.T) {
			authService.role = role
			defer func() { authService.role = iamEntity.UserRole }()
			request := authenticatedWorklistRequest(http.MethodPost, "/worklist/studies/reprocess-batch")
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
