package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	apiError "api-pacs/internal/errors"
	accessEntity "api-pacs/module/access/domain/entity"
	accessRepository "api-pacs/module/access/domain/repository"
	accessService "api-pacs/module/access/infrastructure/service"
	accessRest "api-pacs/module/access/interfaces/http/rest"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	iamService "api-pacs/module/iam/infrastructure/service"
	iamServiceTypes "api-pacs/module/iam/infrastructure/service/types"
	inferenceEntity "api-pacs/module/inference/domain/entity"
	inferenceRepository "api-pacs/module/inference/domain/repository"
	inferenceRepositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	inferenceService "api-pacs/module/inference/infrastructure/service"
	inferenceRest "api-pacs/module/inference/interfaces/http/rest"
	userEntity "api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	userService "api-pacs/module/user/infrastructure/service"
	userRest "api-pacs/module/user/interfaces/http/rest"
)

const (
	demotionTenantID = "tenant-a"
	demotionOwnerID  = "owner-kept"
	demotionAdminID  = "admin-demoted"
	demotionRoleID   = "role-operators"
)

// demotionDirectory is the tenant user document and the custom-role collection
// those records share. UpdateTenantUser writes the same role fields
// GetUserAssignment and SetUserCustomRole read.
type demotionDirectory struct {
	repository.UserQueryRepositoryInterface
	repository.UserCommandRepositoryInterface
	mu    sync.Mutex
	users map[string]repositoryTypes.GetTenantUser
	roles map[string]accessEntity.CustomRole
}

func newDemotionDirectory() *demotionDirectory {
	directory := &demotionDirectory{
		users: map[string]repositoryTypes.GetTenantUser{},
		roles: map[string]accessEntity.CustomRole{},
	}
	directory.users[demotionOwnerID] = repositoryTypes.GetTenantUser{
		ID: demotionOwnerID, TenantID: demotionTenantID, Role: iamEntity.OwnerRole,
		Name: "Owner", LicenseNo: "LIC-OWNER", Specialty: "Cardiology",
		AccessState: userEntity.AccountAccessActive,
	}
	directory.users[demotionAdminID] = repositoryTypes.GetTenantUser{
		ID: demotionAdminID, TenantID: demotionTenantID, Role: iamEntity.AdminRole,
		Name: "Admin", LicenseNo: "LIC-ADMIN", Specialty: "Cardiology",
		AccessState: userEntity.AccountAccessActive,
	}
	directory.roles[demotionRoleID] = accessEntity.CustomRole{
		ID: demotionRoleID, TenantID: demotionTenantID, Name: "Workspace operators",
		Permissions: append([]string{}, accessEntity.GrantablePermissions...), AllModels: true,
	}
	return directory
}

func (directory *demotionDirectory) SelectTenantUserByID(_ context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[id]
	if !ok || user.TenantID != tenantID {
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	return user, nil
}

func (directory *demotionDirectory) SelectUserPolicyAcceptances(_ context.Context, tenantID, userID string, policies []userEntity.PolicyReference) ([]userEntity.UserPolicyAcceptance, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[userID]
	if !ok || user.TenantID != tenantID {
		return nil, errors.New(apiError.MissingRecord)
	}
	accepted := make([]userEntity.UserPolicyAcceptance, 0, len(policies))
	for _, policy := range policies {
		accepted = append(accepted, userEntity.UserPolicyAcceptance{
			TenantID: tenantID, UserID: userID, PolicyKey: policy.PolicyKey, Version: policy.Version,
			AcceptedAt: 1, Source: userEntity.PolicyAcceptanceSourceAuthenticated,
		})
	}
	return accepted, nil
}

// UpdateTenantUser writes the Firestore fields of UserCommandRepository.UpdateTenantUser:
// role, license, specialty, and a cleared custom role when the account leaves USER.
func (directory *demotionDirectory) UpdateTenantUser(_ context.Context, data repositoryTypes.UpdateTenantUser) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[data.ID]
	if !ok || user.TenantID != data.TenantID {
		return errors.New(apiError.MissingRecord)
	}
	user.Role = data.Role
	user.LicenseNo = data.LicenseNo
	user.Specialty = data.Specialty
	user.UpdatedAt = data.UpdatedAt
	if data.Role != iamEntity.UserRole {
		user.CustomRoleID = ""
	}
	directory.users[data.ID] = user
	return nil
}

func (directory *demotionDirectory) ListRoles(_ context.Context, tenantID string) ([]accessEntity.CustomRole, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	roles := []accessEntity.CustomRole{}
	for _, role := range directory.roles {
		if role.TenantID == tenantID {
			roles = append(roles, role)
		}
	}
	return roles, nil
}

func (directory *demotionDirectory) GetRole(_ context.Context, tenantID, roleID string) (accessEntity.CustomRole, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	role, ok := directory.roles[roleID]
	if !ok || role.TenantID != tenantID {
		return accessEntity.CustomRole{}, errors.New(apiError.MissingRecord)
	}
	return role, nil
}

func (directory *demotionDirectory) CreateRole(_ context.Context, role accessEntity.CustomRole) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	if _, exists := directory.roles[role.ID]; exists {
		return errors.New(apiError.DuplicateRecord)
	}
	directory.roles[role.ID] = role
	return nil
}

func (directory *demotionDirectory) UpdateRole(_ context.Context, role accessEntity.CustomRole) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	existing, ok := directory.roles[role.ID]
	if !ok || existing.TenantID != role.TenantID {
		return errors.New(apiError.MissingRecord)
	}
	directory.roles[role.ID] = role
	return nil
}

func (directory *demotionDirectory) DeleteRole(_ context.Context, tenantID, roleID string) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	for _, user := range directory.users {
		if user.TenantID == tenantID && user.CustomRoleID == roleID {
			return errors.New(apiError.RoleInUse)
		}
	}
	role, ok := directory.roles[roleID]
	if !ok || role.TenantID != tenantID {
		return errors.New(apiError.MissingRecord)
	}
	delete(directory.roles, roleID)
	return nil
}

func (directory *demotionDirectory) GetUserAssignment(_ context.Context, tenantID, userID string) (accessRepository.UserRoleAssignment, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[userID]
	if !ok || user.TenantID != tenantID {
		return accessRepository.UserRoleAssignment{}, errors.New(apiError.MissingRecord)
	}
	return accessRepository.UserRoleAssignment{
		UserID: user.ID, TenantID: user.TenantID, Role: user.Role, CustomRoleID: user.CustomRoleID,
	}, nil
}

func (directory *demotionDirectory) ListUserAssignments(_ context.Context, tenantID string) ([]accessRepository.UserRoleAssignment, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	assignments := []accessRepository.UserRoleAssignment{}
	for _, user := range directory.users {
		if user.TenantID == tenantID {
			assignments = append(assignments, accessRepository.UserRoleAssignment{
				UserID: user.ID, TenantID: user.TenantID, Role: user.Role, CustomRoleID: user.CustomRoleID,
			})
		}
	}
	return assignments, nil
}

// SetUserCustomRole applies the transaction in RoleRepository.SetUserCustomRole:
// the role must belong to the tenant, and a non-empty assignment requires USER.
func (directory *demotionDirectory) SetUserCustomRole(_ context.Context, tenantID, userID, roleID string) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[userID]
	if !ok || user.TenantID != tenantID {
		return errors.New(apiError.MissingRecord)
	}
	if roleID != "" {
		role, exists := directory.roles[roleID]
		if !exists || role.TenantID != tenantID {
			return errors.New(apiError.MissingRecord)
		}
		if user.Role != iamEntity.UserRole {
			return errors.New(apiError.InvalidPayload)
		}
	}
	user.CustomRoleID = roleID
	directory.users[userID] = user
	return nil
}

func (directory *demotionDirectory) snapshot() (repositoryTypes.GetTenantUser, accessEntity.CustomRole, int) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	return directory.users[demotionAdminID], directory.roles[demotionRoleID], len(directory.roles)
}

type demotionActions struct {
	mu    sync.Mutex
	names []string
}

func (actions *demotionActions) handler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		actions.mu.Lock()
		actions.names = append(actions.names, name)
		actions.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (actions *demotionActions) called() []string {
	actions.mu.Lock()
	defer actions.mu.Unlock()
	return append([]string{}, actions.names...)
}

// demotionPredictions records the exam and report queries the predictions
// service issues after it authorizes the caller.
type demotionPredictions struct {
	mu      sync.Mutex
	exams   []inferenceRepositoryTypes.ListPredictionExams
	reports []inferenceRepositoryTypes.CountReturnedReports
}

func (store *demotionPredictions) ListPredictionExams(_ context.Context, data inferenceRepositoryTypes.ListPredictionExams) (inferenceRepositoryTypes.PredictionExamPage, error) {
	store.mu.Lock()
	store.exams = append(store.exams, data)
	store.mu.Unlock()
	return inferenceRepositoryTypes.PredictionExamPage{Exams: []inferenceRepositoryTypes.PredictionExamRow{}}, nil
}

func (store *demotionPredictions) ListPredictionExecutions(context.Context, inferenceRepositoryTypes.ListPredictionExecutions) ([]inferenceRepositoryTypes.PredictionExecutionRow, error) {
	return []inferenceRepositoryTypes.PredictionExecutionRow{}, nil
}

func (store *demotionPredictions) ListPredictionModelHistory(context.Context, inferenceRepositoryTypes.ListPredictionModelHistory) ([]inferenceRepositoryTypes.PredictionExecutionRow, error) {
	return []inferenceRepositoryTypes.PredictionExecutionRow{}, nil
}

func (store *demotionPredictions) ListPredictionLaunchers(context.Context, string, []string) ([]inferenceRepositoryTypes.PredictionLauncherRow, error) {
	return []inferenceRepositoryTypes.PredictionLauncherRow{}, nil
}

func (store *demotionPredictions) CountReturnedReports(_ context.Context, data inferenceRepositoryTypes.CountReturnedReports) ([]inferenceRepositoryTypes.ReturnedReportCountRow, error) {
	store.mu.Lock()
	store.reports = append(store.reports, data)
	store.mu.Unlock()
	return []inferenceRepositoryTypes.ReturnedReportCountRow{}, nil
}

func (store *demotionPredictions) queriedEveryExam() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, query := range store.exams {
		if query.Scope == inferenceRepositoryTypes.PredictionsScopeAll {
			return true
		}
	}
	return false
}

func (store *demotionPredictions) countedReports() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.reports) > 0
}

type demotionFeedbackReads struct {
	inferenceRepository.InferenceQueryRepositoryInterface
	mu    sync.Mutex
	calls int
}

func (reads *demotionFeedbackReads) SelectModelFeedbacksByTenantID(context.Context, string) ([]inferenceEntity.ModelFeedback, error) {
	reads.mu.Lock()
	reads.calls++
	reads.mu.Unlock()
	return []inferenceEntity.ModelFeedback{}, nil
}

func (reads *demotionFeedbackReads) read() bool {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return reads.calls > 0
}

func issueDemotionSession(t *testing.T, command *iamService.IAMCommandService, user repositoryTypes.GetTenantUser) string {
	t.Helper()
	sessionID := ksuid.New().String()
	require.NoError(t, command.SetTokenSession(context.Background(), iamServiceTypes.SetTokenSession{
		SessionID: sessionID, TenantID: user.TenantID, UserID: user.ID, Role: user.Role,
		ExpireTimeInSeconds: iamEntity.ExpireTimeInSeconds,
	}))
	return sessionID
}

func demotionRequest(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func requireDemotionDenied(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, recorder.Code, recorder.Body.String())
}

func allPermissionRoleBody(t *testing.T, name string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"name": name, "description": "every grantable permission",
		"permissions": accessEntity.GrantablePermissions, "allModels": true,
	})
	require.NoError(t, err)
	return string(payload)
}

// TestDemotedAdminSessionRetainsWorkspaceControl follows an ADMIN session issued
// the way login and the account handoff store a role, then the owner demotes
// that account through PUT /v1/user/update. The same bearer must not keep
// workspace permissions, open every exam, or install a custom role on itself.
func TestDemotedAdminSessionRetainsWorkspaceControl(t *testing.T) {
	directory := newDemotionDirectory()
	redis := newLoginAuditRedis()
	commandRepository := &iamRepository.IAMCommandRepository{RedisDBHandlerInterface: redis}
	queryRepository := &iamRepository.IAMQueryRepository{RedisDBHandlerInterface: redis}
	iamCommand := &iamService.IAMCommandService{IAMCommandRepositoryInterface: commandRepository}
	iamQuery := &iamService.IAMQueryService{IAMQueryRepositoryInterface: queryRepository}
	access := &accessService.AccessService{Repository: directory, TTL: time.Second}
	predictionsStore := &demotionPredictions{}
	feedbackReads := &demotionFeedbackReads{}
	actions := &demotionActions{}
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: iamCommand,
		IAMQueryServiceInterface:   iamQuery,
		UserQueryServiceInterface: &userService.UserQueryService{
			UserQueryRepositoryInterface: directory,
			PolicyCatalog:                userService.PolicyCatalogFromEnvironment(),
		},
		AccessResolver: access,
	}
	userController := userRest.UserCommandController{
		UserCommandServiceInterface: &userService.UserCommandService{
			UserCommandRepositoryInterface: directory,
			UserQueryRepositoryInterface:   directory,
			IAMCommandServiceInterface:     iamCommand,
			TenantQueryServiceInterface:    memberTenantLookup{},
		},
	}
	accessController := accessRest.AccessController{Roles: access}
	predictions := inferenceRest.InferencePredictionsController{
		InferencePredictionsServiceInterface: &inferenceService.InferencePredictionsService{Repository: predictionsStore},
	}
	feedback := inferenceRest.InferenceQueryController{
		InferenceQueryServiceInterface: &inferenceService.InferenceQueryService{
			InferenceQueryRepositoryInterface: feedbackReads,
		},
	}

	router := chi.NewRouter()
	router.Route("/v1", func(r chi.Router) {
		r.Route("/ecs", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			r.Use(middleware.RequirePermission(accessEntity.PermLogsView))
			r.Get("/logs", actions.handler("logs.view"))
		})
		r.Route("/inference", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(middleware.TokenSessionAuthGuard)
				r.Use(middleware.PolicyAcceptanceGuard)
				r.With(middleware.RequirePermission(accessEntity.PermModelsManage)).Post("/model/add", actions.handler("models.manage"))
				r.With(middleware.RequirePermission(accessEntity.PermModelsManage)).Get("/gpus", actions.handler("models.manage gpus"))
				r.Route("/model", func(r chi.Router) {
					mountInferenceFeedbackAdminRoutes(r, middleware.RequirePermission(accessEntity.PermFeedbackView), inferenceFeedbackAdminHandlers{
						list:         feedback.ListModelFeedback,
						reportCounts: predictions.ListFeedbackReportCounts,
					})
				})
				r.Route("/ingestion", func(r chi.Router) {
					r.Use(middleware.RequirePermission(accessEntity.PermProcessingManage))
					r.Use(middleware.RequireAllModels)
					r.Post("/job/create", actions.handler("processing.manage"))
				})
				mountInferenceWorklistRoutes(r, middleware.TokenSessionAuthGuard, middleware.PolicyAcceptanceGuard,
					middleware.RequirePermission(accessEntity.PermModelsUse),
					middleware.RequirePermission(accessEntity.PermProcessingManage),
					middleware.RequireAllModels,
					inferenceWorklistHandlers{
						reprocessStudy:   actions.handler("processing.manage reprocess"),
						reprocessStudies: actions.handler("processing.manage reprocess batch"),
					},
				)
				mountInferencePredictionsRoutes(r, middleware.TokenSessionAuthGuard, middleware.PolicyAcceptanceGuard, inferencePredictionsHandlers{
					list: predictions.ListPredictions,
				})
			})
		})
		r.Route("/orthanc", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			r.Use(middleware.PolicyAcceptanceGuard)
			r.With(middleware.RequirePermission(accessEntity.PermDicomManage)).Post("/modality/{modalityID}/echo", actions.handler("dicom.manage"))
		})
		r.Route("/tenant", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			r.With(middleware.RequirePermission(accessEntity.PermWorkspaceManage)).Put("/onboarding-consent/config/update", actions.handler("workspace.manage"))
		})
		r.Route("/user", func(r chi.Router) {
			r.Use(middleware.TokenSessionAuthGuard)
			mountUserMemberRoutes(r, userMemberGuards{
				invite:       middleware.RequirePermission(accessEntity.PermMembersInvite),
				manage:       middleware.RequirePermission(accessEntity.PermMembersManage),
				ownerOrAdmin: middleware.RBACOwnerOrAdminGuard,
			}, userMemberHandlers{
				add:          actions.handler("members.invite add"),
				invite:       actions.handler("members.invite"),
				resendInvite: actions.handler("members.invite resend"),
				listInvites:  actions.handler("members.invite list"),
				removeInvite: actions.handler("members.invite remove"),
				listUsers:    actions.handler("members.manage"),
				policyStatus: actions.handler("members.manage policy"),
				update:       userController.UpdateTenantUser,
				suspend:      actions.handler("members.manage suspend"),
				reactivate:   actions.handler("members.manage reactivate"),
				remove:       actions.handler("members.manage remove"),
				myAccess:     accessController.GetMyAccess,
				listRoles:    accessController.ListRoles,
				createRole:   accessController.CreateRole,
				updateRole:   accessController.UpdateRole,
				deleteRole:   accessController.DeleteRole,
				assignRole:   accessController.AssignCustomRole,
			})
		})
	})

	owner, err := directory.SelectTenantUserByID(context.Background(), demotionTenantID, demotionOwnerID)
	require.NoError(t, err)
	admin, err := directory.SelectTenantUserByID(context.Background(), demotionTenantID, demotionAdminID)
	require.NoError(t, err)
	ownerSession := issueDemotionSession(t, iamCommand, owner)
	// Login and the account handoff each call SetTokenSession with the role on
	// the user record. Both sessions are outstanding when the owner demotes.
	loginSession := issueDemotionSession(t, iamCommand, admin)
	handoffSession := issueDemotionSession(t, iamCommand, admin)

	demotion := demotionRequest(router, http.MethodPut, "/v1/user/update", ownerSession, `{
		"id":"`+demotionAdminID+`","role":"USER","name":"Former Admin","licenseNo":"LIC-ADMIN","specialty":"Cardiology"
	}`)
	require.Equal(t, http.StatusOK, demotion.Code, demotion.Body.String())
	demoted, seeded, roleCount := directory.snapshot()
	require.Equal(t, iamEntity.UserRole, demoted.Role)
	require.Equal(t, "", demoted.CustomRoleID)
	require.Equal(t, "Workspace operators", seeded.Name)
	require.Equal(t, 1, roleCount)

	privileged := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "models.manage", method: http.MethodPost, path: "/v1/inference/model/add", body: `{"name":"extra"}`},
		{name: "models.manage gpus", method: http.MethodGet, path: "/v1/inference/gpus"},
		{name: "dicom.manage", method: http.MethodPost, path: "/v1/orthanc/modality/modality-1/echo"},
		{name: "logs.view", method: http.MethodGet, path: "/v1/ecs/logs"},
		{name: "members.invite", method: http.MethodPost, path: "/v1/user/invite", body: `{"email":"new@example.com"}`},
		{name: "members.manage", method: http.MethodGet, path: "/v1/user/all"},
		{name: "processing.manage", method: http.MethodPost, path: "/v1/inference/ingestion/job/create", body: `{}`},
		{name: "processing.manage reprocess", method: http.MethodPost, path: "/v1/inference/worklist/studies/1.2.3/reprocess"},
		{name: "workspace.manage", method: http.MethodPut, path: "/v1/tenant/onboarding-consent/config/update", body: `{}`},
	}
	for _, session := range []struct {
		name  string
		token string
	}{
		{name: "login", token: loginSession},
		{name: "handoff", token: handoffSession},
	} {
		for _, call := range privileged {
			response := demotionRequest(router, call.method, call.path, session.token, call.body)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
				t.Errorf("%s %s stayed authorized with status %d", session.name, call.name, response.Code)
			}
		}
		create := demotionRequest(router, http.MethodPost, "/v1/user/roles", session.token, allPermissionRoleBody(t, "Retained admin "+session.name))
		if create.Code != http.StatusUnauthorized && create.Code != http.StatusForbidden {
			t.Errorf("create role stayed authorized with status %d body %s", create.Code, create.Body.String())
		}
		assign := demotionRequest(router, http.MethodPut, "/v1/user/"+demotionAdminID+"/custom-role", session.token, `{"customRoleId":"`+demotionRoleID+`"}`)
		if assign.Code != http.StatusUnauthorized && assign.Code != http.StatusForbidden {
			t.Errorf("assign custom role stayed authorized with status %d body %s", assign.Code, assign.Body.String())
		}
		update := demotionRequest(router, http.MethodPut, "/v1/user/roles/"+demotionRoleID, session.token, allPermissionRoleBody(t, "Renamed operators"))
		if update.Code != http.StatusUnauthorized && update.Code != http.StatusForbidden {
			t.Errorf("update role stayed authorized with status %d body %s", update.Code, update.Body.String())
		}
		remove := demotionRequest(router, http.MethodDelete, "/v1/user/roles/"+demotionRoleID, session.token, "")
		if remove.Code != http.StatusUnauthorized && remove.Code != http.StatusForbidden {
			t.Errorf("delete role stayed authorized with status %d body %s", remove.Code, remove.Body.String())
		}
		exams := demotionRequest(router, http.MethodGet, "/v1/inference/predictions?view=all", session.token, "")
		if exams.Code != http.StatusUnauthorized && exams.Code != http.StatusForbidden {
			t.Errorf("predictions view=all stayed authorized with status %d", exams.Code)
		}
		reports := demotionRequest(router, http.MethodGet, "/v1/inference/model/feedback/report-counts?from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z", session.token, "")
		if reports.Code != http.StatusUnauthorized && reports.Code != http.StatusForbidden {
			t.Errorf("feedback report counts stayed authorized with status %d", reports.Code)
		}
		votes := demotionRequest(router, http.MethodGet, "/v1/inference/model/feedback/list", session.token, "")
		if votes.Code != http.StatusUnauthorized && votes.Code != http.StatusForbidden {
			t.Errorf("model feedback list stayed authorized with status %d", votes.Code)
		}
		accessView := demotionRequest(router, http.MethodGet, "/v1/user/me/access", session.token, "")
		requireDemotedBaseline(t, accessView)
	}

	if called := actions.called(); len(called) > 0 {
		t.Errorf("privileged handlers ran: %s", strings.Join(called, ", "))
	}
	if predictionsStore.queriedEveryExam() {
		t.Error("predictions listed every exam")
	}
	if predictionsStore.countedReports() {
		t.Error("feedback report counts queried the tenant")
	}
	if feedbackReads.read() {
		t.Error("model feedback listed the tenant")
	}
	demoted, seeded, roleCount = directory.snapshot()
	if demoted.Role != iamEntity.UserRole {
		t.Errorf("stored role %s", demoted.Role)
	}
	if demoted.CustomRoleID != "" {
		t.Errorf("custom role %s assigned to the demoted account", demoted.CustomRoleID)
	}
	if seeded.Name != "Workspace operators" {
		t.Errorf("seeded role renamed to %s", seeded.Name)
	}
	if roleCount != 1 {
		t.Errorf("role count %d", roleCount)
	}

	// A later sign-in stores the role now on the user record, the same write login uses.
	current, err := directory.SelectTenantUserByID(context.Background(), demotionTenantID, demotionAdminID)
	require.NoError(t, err)
	require.Equal(t, iamEntity.UserRole, current.Role)
	freshSession := issueDemotionSession(t, iamCommand, current)
	freshAccess := demotionRequest(router, http.MethodGet, "/v1/user/me/access", freshSession, "")
	requireDemotedBaseline(t, freshAccess)
	freshExams := demotionRequest(router, http.MethodGet, "/v1/inference/predictions?view=all", freshSession, "")
	requireDemotionDenied(t, freshExams)
	freshModels := demotionRequest(router, http.MethodPost, "/v1/inference/model/add", freshSession, `{"name":"extra"}`)
	requireDemotionDenied(t, freshModels)
	require.Empty(t, actions.called())
	require.False(t, predictionsStore.queriedEveryExam())
}

func requireDemotedBaseline(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
		return
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("access lookup status %d body %s", recorder.Code, recorder.Body.String())
		return
	}
	var body struct {
		Data struct {
			Role        string   `json:"role"`
			Permissions []string `json:"permissions"`
			AllModels   bool     `json:"allModels"`
			CustomRole  *struct {
				ID string `json:"id"`
			} `json:"customRole"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Errorf("access lookup body: %v", err)
		return
	}
	if body.Data.Role != iamEntity.UserRole {
		t.Errorf("access role %s", body.Data.Role)
	}
	if body.Data.CustomRole != nil {
		t.Errorf("access still carries custom role %s", body.Data.CustomRole.ID)
	}
	for _, permission := range accessEntity.GrantablePermissions {
		for _, held := range body.Data.Permissions {
			if held == permission {
				t.Errorf("access still holds %s", permission)
			}
		}
	}
}
