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
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	iamEntity "api-pacs/module/iam/domain/entity"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

type stubAccessResolver struct {
	custom *accessEntity.CustomRole
	err    error
	calls  int
}

func (resolver *stubAccessResolver) Resolve(_ context.Context, _, _, role string) (accessEntity.Access, error) {
	resolver.calls++
	if resolver.err != nil {
		return accessEntity.Access{}, resolver.err
	}
	if role != iamEntity.UserRole {
		return accessEntity.ForRole(role, nil), nil
	}
	return accessEntity.ForRole(role, resolver.custom), nil
}

func memberRouter(role string, resolver iamMiddleware.AccessResolver) http.Handler {
	router := chi.NewRouter()
	respond := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: role},
		UserQueryServiceInterface: &worklistPolicyQueryService{
			status: userServiceTypes.PolicyStatus{EnforcementActive: true},
		},
		AccessResolver: resolver,
	}
	router.Route("/user", func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		mountUserMemberRoutes(r, userMemberGuards{
			invite:       middleware.RequirePermission(accessEntity.PermMembersInvite),
			manage:       middleware.RequirePermission(accessEntity.PermMembersManage),
			ownerOrAdmin: middleware.RBACOwnerOrAdminGuard,
		}, userMemberHandlers{
			add: respond, invite: respond, resendInvite: respond, listInvites: respond, removeInvite: respond,
			listUsers: respond, policyStatus: respond, update: respond, suspend: respond, reactivate: respond,
			remove: respond, myAccess: respond, listRoles: respond, createRole: respond, updateRole: respond,
			deleteRole: respond, assignRole: respond,
		})
	})
	return router
}

var (
	inviteRoutes = [][2]string{
		{http.MethodPost, "/user/add"}, {http.MethodPost, "/user/invite"}, {http.MethodPost, "/user/invite/resend"},
		{http.MethodGet, "/user/invites"}, {http.MethodDelete, "/user/invite/i1/remove"},
	}
	manageRoutes = [][2]string{
		{http.MethodGet, "/user/all"}, {http.MethodGet, "/user/roles"}, {http.MethodGet, "/user/u2/policies/status"},
		{http.MethodPut, "/user/update"}, {http.MethodPost, "/user/u2/suspend"}, {http.MethodPost, "/user/u2/reactivate"},
		{http.MethodDelete, "/user/u2/remove"},
	}
	roleAdminRoutes = [][2]string{
		{http.MethodPost, "/user/roles"}, {http.MethodPut, "/user/roles/r1"}, {http.MethodDelete, "/user/roles/r1"},
		{http.MethodPut, "/user/u2/custom-role"},
	}
)

func serve(handler http.Handler, method, path string) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedWorklistRequest(method, path))
	return recorder.Code
}

func requireRoutes(t *testing.T, handler http.Handler, routes [][2]string, status int, label string) {
	t.Helper()
	for _, route := range routes {
		require.Equal(t, status, serve(handler, route[0], route[1]), "%s %s %s", label, route[0], route[1])
	}
}

func TestMemberRoutesForOwnerAndAdmin(t *testing.T) {
	for _, role := range []string{iamEntity.OwnerRole, iamEntity.AdminRole} {
		router := memberRouter(role, &stubAccessResolver{})
		requireRoutes(t, router, inviteRoutes, http.StatusNoContent, role)
		requireRoutes(t, router, manageRoutes, http.StatusNoContent, role)
		requireRoutes(t, router, roleAdminRoutes, http.StatusNoContent, role)
		require.Equal(t, http.StatusNoContent, serve(router, http.MethodGet, "/user/me/access"))
	}
}

func TestMemberRoutesForPlainUser(t *testing.T) {
	router := memberRouter(iamEntity.UserRole, &stubAccessResolver{})
	requireRoutes(t, router, inviteRoutes, http.StatusForbidden, "user")
	requireRoutes(t, router, manageRoutes, http.StatusForbidden, "user")
	requireRoutes(t, router, roleAdminRoutes, http.StatusForbidden, "user")
	require.Equal(t, http.StatusNoContent, serve(router, http.MethodGet, "/user/me/access"))
}

func TestMemberRoutesForCustomRoles(t *testing.T) {
	inviter := memberRouter(iamEntity.UserRole, &stubAccessResolver{custom: &accessEntity.CustomRole{
		Permissions: []string{accessEntity.PermMembersInvite},
	}})
	requireRoutes(t, inviter, inviteRoutes, http.StatusNoContent, "inviter")
	requireRoutes(t, inviter, manageRoutes, http.StatusForbidden, "inviter")
	requireRoutes(t, inviter, roleAdminRoutes, http.StatusForbidden, "inviter")

	manager := memberRouter(iamEntity.UserRole, &stubAccessResolver{custom: &accessEntity.CustomRole{
		Permissions: []string{accessEntity.PermMembersManage},
	}})
	requireRoutes(t, manager, inviteRoutes, http.StatusForbidden, "manager")
	requireRoutes(t, manager, manageRoutes, http.StatusNoContent, "manager")
	requireRoutes(t, manager, roleAdminRoutes, http.StatusForbidden, "members.manage never edits or assigns roles")
}

func TestPermissionRoutesFailClosedWhenAccessIsUnavailable(t *testing.T) {
	resolver := &stubAccessResolver{err: errors.New("firestore down")}
	router := memberRouter(iamEntity.UserRole, resolver)
	requireRoutes(t, router, inviteRoutes, http.StatusServiceUnavailable, "unavailable")
	requireRoutes(t, router, manageRoutes, http.StatusServiceUnavailable, "unavailable")
}

func TestAccessIsResolvedLazilyOncePerRequest(t *testing.T) {
	resolver := &stubAccessResolver{}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   &worklistAuthQueryService{role: iamEntity.UserRole},
		AccessResolver:             resolver,
	}
	router := chi.NewRouter()
	router.With(middleware.TokenSessionAuthGuard).Get("/plain", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.With(middleware.TokenSessionAuthGuard, middleware.RequirePermission(accessEntity.PermModelsUse)).Get("/twice", func(w http.ResponseWriter, r *http.Request) {
		require.True(t, accessctx.Has(r.Context(), accessEntity.PermFeedbackVote))
		w.WriteHeader(http.StatusNoContent)
	})

	require.Equal(t, http.StatusNoContent, serve(router, http.MethodGet, "/plain"))
	require.Zero(t, resolver.calls, "routes that never ask do not read the custom role")
	require.Equal(t, http.StatusNoContent, serve(router, http.MethodGet, "/twice"))
	require.Equal(t, 1, resolver.calls)
}

func TestFeedbackDashboardRoutesFollowFeedbackViewPermission(t *testing.T) {
	respond := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	build := func(custom *accessEntity.CustomRole) http.Handler {
		middleware := &iamMiddleware.IAMMiddleware{
			IAMCommandServiceInterface: &worklistAuthCommandService{},
			IAMQueryServiceInterface:   &worklistAuthQueryService{role: iamEntity.UserRole},
			AccessResolver:             &stubAccessResolver{custom: custom},
		}
		router := chi.NewRouter()
		router.Route("/model", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			mountInferenceFeedbackAdminRoutes(r, middleware.RequirePermission(accessEntity.PermFeedbackView), inferenceFeedbackAdminHandlers{
				list: respond, reportCounts: respond,
			})
		})
		return router
	}
	require.Equal(t, http.StatusForbidden, serve(build(nil), http.MethodGet, "/model/feedback/list"))
	viewer := build(&accessEntity.CustomRole{Permissions: []string{accessEntity.PermFeedbackView}})
	require.Equal(t, http.StatusNoContent, serve(viewer, http.MethodGet, "/model/feedback/list"))
	require.Equal(t, http.StatusNoContent, serve(viewer, http.MethodGet, "/model/feedback/report-counts"))
}

func TestReprocessingNeedsProcessingManageAndEveryModel(t *testing.T) {
	respond := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	build := func(role string, custom *accessEntity.CustomRole) http.Handler {
		middleware := &iamMiddleware.IAMMiddleware{
			IAMCommandServiceInterface: &worklistAuthCommandService{},
			IAMQueryServiceInterface:   &worklistAuthQueryService{role: role},
			UserQueryServiceInterface: &worklistPolicyQueryService{
				status: userServiceTypes.PolicyStatus{EnforcementActive: true},
			},
			AccessResolver: &stubAccessResolver{custom: custom},
		}
		router := chi.NewRouter()
		mountInferenceWorklistRoutes(router, middleware.TokenSessionAuthGuard, middleware.PolicyAcceptanceGuard,
			middleware.RequirePermission(accessEntity.PermModelsUse),
			middleware.RequirePermission(accessEntity.PermProcessingManage),
			middleware.RequireAllModels,
			inferenceWorklistHandlers{
				getStatuses: respond, streamEvents: respond, getRunHistory: respond, getRunDetail: respond,
				getExecutionResult: respond, reprocessStudy: respond, reprocessStudies: respond, getLatestResults: respond,
				getExecutionReport: respond, getStudyJobReport: respond, runStudyModel: respond, getStudyMedia: respond,
				getStudyMediaFile: respond, cancelRun: respond,
			})
		return router
	}
	paths := []string{"/worklist/studies/1.2.3/reprocess", "/worklist/studies/reprocess-batch"}
	for _, path := range paths {
		require.Equal(t, http.StatusNoContent, serve(build(iamEntity.AdminRole, nil), http.MethodPost, path))
		require.Equal(t, http.StatusForbidden, serve(build(iamEntity.UserRole, nil), http.MethodPost, path), "no processing.manage")
		require.Equal(t, http.StatusNoContent, serve(build(iamEntity.UserRole, &accessEntity.CustomRole{
			Permissions: []string{accessEntity.PermProcessingManage}, AllModels: true,
		}), http.MethodPost, path))
		require.Equal(t, http.StatusForbidden, serve(build(iamEntity.UserRole, &accessEntity.CustomRole{
			Permissions: []string{accessEntity.PermProcessingManage}, ModelIDs: []string{"PanEcho"},
		}), http.MethodPost, path), "a model-restricted role cannot reprocess every model")
	}
}
