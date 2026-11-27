package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	iamMiddleware "api-pacs/interfaces/http/rest/middlewares/iam"
	apiError "api-pacs/internal/errors"
	accessEntity "api-pacs/module/access/domain/entity"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userEntity "api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	userService "api-pacs/module/user/infrastructure/service"
	userRest "api-pacs/module/user/interfaces/http/rest"
)

// memberDirectory stores tenant accounts the way SelectTenantUserByID reads
// them and the way UpdateTenantUser, UpdateTenantUserAccessState, and
// DeleteTenantUser write them. A second load of an account before that load's
// write reports role USER without changing the stored record. Authorization
// has to use the first load: a later load that says USER must not let the
// write proceed.
type memberDirectory struct {
	repository.UserQueryRepositoryInterface
	repository.UserCommandRepositoryInterface
	mu      sync.Mutex
	users   map[string]repositoryTypes.GetTenantUser
	reads   map[string]int
	updates []repositoryTypes.UpdateTenantUser
	deleted []string
}

func newMemberDirectory(users ...repositoryTypes.GetTenantUser) *memberDirectory {
	directory := &memberDirectory{
		users: map[string]repositoryTypes.GetTenantUser{},
		reads: map[string]int{},
	}
	for _, user := range users {
		directory.users[user.ID] = user
	}
	return directory
}

func activeMember(id, role, name string) repositoryTypes.GetTenantUser {
	return repositoryTypes.GetTenantUser{
		ID: id, TenantID: "tenant-a", Role: role, Name: name,
		LicenseNo: "LIC-" + id, Specialty: "Cardiology",
		AccessState: userEntity.AccountAccessActive,
	}
}

func (directory *memberDirectory) SelectTenantUserByID(_ context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[id]
	if !ok || user.TenantID != tenantID {
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	directory.reads[id]++
	// A repeat load before the write is a different answer than the account
	// that was just read. The stored record is left alone.
	if directory.reads[id] > 1 && !directory.writtenLocked(id) {
		user.Role = iamEntity.UserRole
	}
	return user, nil
}

func (directory *memberDirectory) writtenLocked(id string) bool {
	for _, update := range directory.updates {
		if update.ID == id {
			return true
		}
	}
	return false
}

func (directory *memberDirectory) UpdateTenantUser(_ context.Context, data repositoryTypes.UpdateTenantUser) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[data.ID]
	if !ok || user.TenantID != data.TenantID {
		return errors.New(apiError.MissingRecord)
	}
	// UserCommandRepository.UpdateTenantUser writes the display name, role,
	// license, and specialty, and clears a custom role when the account leaves USER.
	user.Name = data.Name
	user.Role = data.Role
	user.LicenseNo = data.LicenseNo
	user.Specialty = data.Specialty
	if data.Role != iamEntity.UserRole {
		user.CustomRoleID = ""
	}
	directory.users[data.ID] = user
	directory.updates = append(directory.updates, data)
	return nil
}

func (directory *memberDirectory) UpdateTenantUserAccessState(_ context.Context, data repositoryTypes.UpdateTenantUserAccessState) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[data.ID]
	if !ok || user.TenantID != data.TenantID {
		return errors.New(apiError.MissingRecord)
	}
	user.AccessState = data.AccessState
	user.IsAccountDisabled = data.AccessState == userEntity.AccountAccessSuspended
	directory.users[data.ID] = user
	return nil
}

func (directory *memberDirectory) DeleteTenantUser(_ context.Context, tenantID, id string) error {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[id]
	if !ok || user.TenantID != tenantID {
		return errors.New(apiError.MissingRecord)
	}
	delete(directory.users, id)
	directory.deleted = append(directory.deleted, id)
	return nil
}

func (directory *memberDirectory) resetReads() {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	directory.reads = map[string]int{}
}

func (directory *memberDirectory) snapshot(id string) (repositoryTypes.GetTenantUser, bool) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	user, ok := directory.users[id]
	return user, ok
}

func (directory *memberDirectory) mutations() (updates []repositoryTypes.UpdateTenantUser, deleted []string) {
	directory.mu.Lock()
	defer directory.mu.Unlock()
	return append([]repositoryTypes.UpdateTenantUser{}, directory.updates...), append([]string{}, directory.deleted...)
}

type memberAccessIAM struct {
	iamApplication.IAMCommandServiceInterface
	mu        sync.Mutex
	suspended []string
	revoked   []string
	cleared   []string
}

func (iam *memberAccessIAM) AcquireUserAccessTransition(context.Context, string, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (iam *memberAccessIAM) ReleaseUserAccessTransition(context.Context, string, string, string) error {
	return nil
}

func (iam *memberAccessIAM) SetUserSuspended(_ context.Context, _, userID string) (bool, error) {
	iam.mu.Lock()
	defer iam.mu.Unlock()
	iam.suspended = append(iam.suspended, userID)
	return true, nil
}

func (iam *memberAccessIAM) RevokeUserSessions(_ context.Context, _, userID string) error {
	iam.mu.Lock()
	defer iam.mu.Unlock()
	iam.revoked = append(iam.revoked, userID)
	return nil
}

func (iam *memberAccessIAM) ClearUserSuspension(_ context.Context, _, userID string) error {
	iam.mu.Lock()
	defer iam.mu.Unlock()
	iam.cleared = append(iam.cleared, userID)
	return nil
}

func (iam *memberAccessIAM) effects() (suspended, revoked, cleared []string) {
	iam.mu.Lock()
	defer iam.mu.Unlock()
	return append([]string{}, iam.suspended...), append([]string{}, iam.revoked...), append([]string{}, iam.cleared...)
}

type memberTenantLookup struct {
	tenantApplication.TenantQueryServiceInterface
}

func (memberTenantLookup) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{}, errors.New(apiError.MissingRecord)
}

type memberSession struct {
	iamApplication.IAMQueryServiceInterface
	userID string
	role   string
}

func (session *memberSession) GetSessionToken(_ context.Context, token string) (iamEntity.TokenSession, error) {
	if token != "session" {
		return iamEntity.TokenSession{}, errors.New(apiError.UnauthorizedAccess)
	}
	return iamEntity.TokenSession{TenantID: "tenant-a", UserID: session.userID, Role: session.role}, nil
}

func (*memberSession) IsUserSuspended(context.Context, string, string) (bool, error) {
	return false, nil
}

func memberCommandRouter(directory *memberDirectory, iam *memberAccessIAM, session *memberSession) http.Handler {
	middleware := &iamMiddleware.IAMMiddleware{
		IAMCommandServiceInterface: &worklistAuthCommandService{},
		IAMQueryServiceInterface:   session,
		AccessResolver:             &stubAccessResolver{},
	}
	controller := userRest.UserCommandController{
		UserCommandServiceInterface: &userService.UserCommandService{
			UserCommandRepositoryInterface: directory,
			UserQueryRepositoryInterface:   directory,
			IAMCommandServiceInterface:     iam,
			TenantQueryServiceInterface:    memberTenantLookup{},
		},
	}
	refuse := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected member route", http.StatusInternalServerError)
	}
	router := chi.NewRouter()
	router.Route("/v1/user", func(r chi.Router) {
		r.Use(middleware.TokenSessionAuthGuard)
		mountUserMemberRoutes(r, userMemberGuards{
			invite:       middleware.RequirePermission(accessEntity.PermMembersInvite),
			manage:       middleware.RequirePermission(accessEntity.PermMembersManage),
			ownerOrAdmin: middleware.RBACOwnerOrAdminGuard,
		}, userMemberHandlers{
			add: refuse, invite: refuse, resendInvite: refuse, listInvites: refuse, removeInvite: refuse,
			listUsers: refuse, policyStatus: refuse, myAccess: refuse, listRoles: refuse,
			createRole: refuse, updateRole: refuse, deleteRole: refuse, assignRole: refuse,
			update:     controller.UpdateTenantUser,
			suspend:    controller.SuspendTenantUser,
			reactivate: controller.ReactivateTenantUser,
			remove:     controller.DeleteTenantUser,
		})
	})
	return router
}

func memberRequest(directory *memberDirectory, router http.Handler, session *memberSession, method, path, actorID, actorRole string, body any) (int, string) {
	session.userID = actorID
	session.role = actorRole
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err.Error()
		}
		reader = bytes.NewReader(payload)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer session")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	// The next request has to see the stored account, not a repeat-load of this one.
	directory.resetReads()
	return recorder.Code, recorder.Body.String()
}

func roleUpdateBody(id, role, name string) map[string]string {
	return map[string]string{
		"id": id, "role": role, "name": name, "licenseNo": "LIC-CHANGED", "specialty": "Other",
	}
}

func TestAdminCannotDemoteOwnerThenSuspendReactivateOrDelete(t *testing.T) {
	owner := activeMember("owner", iamEntity.OwnerRole, "Original Owner")
	directory := newMemberDirectory(
		owner,
		activeMember("admin", iamEntity.AdminRole, "Admin"),
	)
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "admin", role: iamEntity.AdminRole}
	router := memberCommandRouter(directory, iam, session)

	updateCode, updateBody := memberRequest(directory, router, session, http.MethodPut, "/v1/user/update", "admin", iamEntity.AdminRole, roleUpdateBody("owner", iamEntity.UserRole, "Taken Over"))
	suspendCode, suspendBody := memberRequest(directory, router, session, http.MethodPost, "/v1/user/owner/suspend", "admin", iamEntity.AdminRole, map[string]string{})
	reactivateCode, reactivateBody := memberRequest(directory, router, session, http.MethodPost, "/v1/user/owner/reactivate", "admin", iamEntity.AdminRole, map[string]string{})
	deleteCode, deleteBody := memberRequest(directory, router, session, http.MethodDelete, "/v1/user/owner/remove", "admin", iamEntity.AdminRole, nil)

	stored, ok := directory.snapshot("owner")
	require.Truef(t, ok, "owner was removed; update=%d %s suspend=%d %s reactivate=%d %s delete=%d %s", updateCode, updateBody, suspendCode, suspendBody, reactivateCode, reactivateBody, deleteCode, deleteBody)
	require.Equal(t, owner, stored)
	updates, deleted := directory.mutations()
	require.Empty(t, updates)
	require.Empty(t, deleted)
	suspended, revoked, cleared := iam.effects()
	require.Empty(t, suspended)
	require.Empty(t, revoked)
	require.Empty(t, cleared)
	require.Equal(t, http.StatusForbidden, updateCode, updateBody)
	require.Equal(t, http.StatusForbidden, suspendCode, suspendBody)
	require.Equal(t, http.StatusForbidden, reactivateCode, reactivateBody)
	require.Equal(t, http.StatusForbidden, deleteCode, deleteBody)
}

func TestAdminCannotDemoteAnotherAdminThenSuspendOrDelete(t *testing.T) {
	peer := activeMember("admin-2", iamEntity.AdminRole, "Peer Admin")
	directory := newMemberDirectory(
		peer,
		activeMember("admin", iamEntity.AdminRole, "Admin"),
		activeMember("owner", iamEntity.OwnerRole, "Original Owner"),
	)
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "admin", role: iamEntity.AdminRole}
	router := memberCommandRouter(directory, iam, session)

	updateCode, updateBody := memberRequest(directory, router, session, http.MethodPut, "/v1/user/update", "admin", iamEntity.AdminRole, roleUpdateBody("admin-2", iamEntity.UserRole, "Taken Over"))
	suspendCode, _ := memberRequest(directory, router, session, http.MethodPost, "/v1/user/admin-2/suspend", "admin", iamEntity.AdminRole, map[string]string{})
	deleteCode, _ := memberRequest(directory, router, session, http.MethodDelete, "/v1/user/admin-2/remove", "admin", iamEntity.AdminRole, nil)

	stored, ok := directory.snapshot("admin-2")
	require.True(t, ok, updateBody)
	require.Equal(t, peer, stored)
	owner, _ := directory.snapshot("owner")
	require.Equal(t, iamEntity.OwnerRole, owner.Role)
	updates, deleted := directory.mutations()
	require.Empty(t, updates)
	require.Empty(t, deleted)
	suspended, _, _ := iam.effects()
	require.Empty(t, suspended)
	require.Equal(t, http.StatusForbidden, updateCode, updateBody)
	require.Equal(t, http.StatusForbidden, suspendCode)
	require.Equal(t, http.StatusForbidden, deleteCode)
}

func TestAdminCannotDemoteSelf(t *testing.T) {
	admin := activeMember("admin", iamEntity.AdminRole, "Admin")
	directory := newMemberDirectory(admin, activeMember("owner", iamEntity.OwnerRole, "Original Owner"))
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "admin", role: iamEntity.AdminRole}
	router := memberCommandRouter(directory, iam, session)

	code, body := memberRequest(directory, router, session, http.MethodPut, "/v1/user/update", "admin", iamEntity.AdminRole, roleUpdateBody("admin", iamEntity.UserRole, "Taken Over"))

	stored, ok := directory.snapshot("admin")
	require.True(t, ok)
	require.Equal(t, admin, stored)
	updates, _ := directory.mutations()
	require.Empty(t, updates)
	require.Equal(t, http.StatusForbidden, code, body)
}

func TestOwnerCannotDemoteAnotherOwner(t *testing.T) {
	other := activeMember("owner-2", iamEntity.OwnerRole, "Other Owner")
	directory := newMemberDirectory(
		other,
		activeMember("owner", iamEntity.OwnerRole, "Original Owner"),
	)
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "owner", role: iamEntity.OwnerRole}
	router := memberCommandRouter(directory, iam, session)

	updateCode, updateBody := memberRequest(directory, router, session, http.MethodPut, "/v1/user/update", "owner", iamEntity.OwnerRole, roleUpdateBody("owner-2", iamEntity.UserRole, "Taken Over"))
	suspendCode, _ := memberRequest(directory, router, session, http.MethodPost, "/v1/user/owner-2/suspend", "owner", iamEntity.OwnerRole, map[string]string{})

	stored, ok := directory.snapshot("owner-2")
	require.True(t, ok, updateBody)
	require.Equal(t, other, stored)
	updates, _ := directory.mutations()
	require.Empty(t, updates)
	suspended, _, _ := iam.effects()
	require.Empty(t, suspended)
	require.Equal(t, http.StatusForbidden, updateCode, updateBody)
	require.Equal(t, http.StatusForbidden, suspendCode)
}

func TestAdminCanUpdateUserWithoutTouchingOwner(t *testing.T) {
	user := activeMember("user-b", iamEntity.UserRole, "Clinician")
	owner := activeMember("owner", iamEntity.OwnerRole, "Original Owner")
	directory := newMemberDirectory(user, owner, activeMember("admin", iamEntity.AdminRole, "Admin"))
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "admin", role: iamEntity.AdminRole}
	router := memberCommandRouter(directory, iam, session)

	code, body := memberRequest(directory, router, session, http.MethodPut, "/v1/user/update", "admin", iamEntity.AdminRole, roleUpdateBody("user-b", iamEntity.UserRole, "Renamed Clinician"))

	require.Equal(t, http.StatusOK, code, body)
	stored, ok := directory.snapshot("user-b")
	require.True(t, ok)
	require.Equal(t, "Renamed Clinician", stored.Name)
	require.Equal(t, iamEntity.UserRole, stored.Role)
	require.Equal(t, "LIC-CHANGED", stored.LicenseNo)
	require.Equal(t, "Other", stored.Specialty)
	untouched, _ := directory.snapshot("owner")
	require.Equal(t, owner, untouched)
	updates, deleted := directory.mutations()
	require.Len(t, updates, 1)
	require.Equal(t, "user-b", updates[0].ID)
	require.Equal(t, iamEntity.UserRole, updates[0].Role)
	require.Empty(t, deleted)
}

func TestOwnerCanDemoteAdmin(t *testing.T) {
	admin := activeMember("admin", iamEntity.AdminRole, "Admin")
	directory := newMemberDirectory(admin, activeMember("owner", iamEntity.OwnerRole, "Original Owner"))
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "owner", role: iamEntity.OwnerRole}
	router := memberCommandRouter(directory, iam, session)

	code, body := memberRequest(directory, router, session, http.MethodPut, "/v1/user/update", "owner", iamEntity.OwnerRole, roleUpdateBody("admin", iamEntity.UserRole, "Now User"))

	require.Equal(t, http.StatusOK, code, body)
	stored, ok := directory.snapshot("admin")
	require.True(t, ok)
	require.Equal(t, iamEntity.UserRole, stored.Role)
	require.Equal(t, "Now User", stored.Name)
	updates, _ := directory.mutations()
	require.Len(t, updates, 1)
	require.Equal(t, "admin", updates[0].ID)
	require.Equal(t, iamEntity.UserRole, updates[0].Role)
}

func TestAdminCannotSuspendOrDeleteOwnerWhileRoleStaysOwner(t *testing.T) {
	owner := activeMember("owner", iamEntity.OwnerRole, "Original Owner")
	directory := newMemberDirectory(owner, activeMember("admin", iamEntity.AdminRole, "Admin"))
	iam := &memberAccessIAM{}
	session := &memberSession{userID: "admin", role: iamEntity.AdminRole}
	router := memberCommandRouter(directory, iam, session)

	suspendCode, suspendBody := memberRequest(directory, router, session, http.MethodPost, "/v1/user/owner/suspend", "admin", iamEntity.AdminRole, map[string]string{})
	deleteCode, deleteBody := memberRequest(directory, router, session, http.MethodDelete, "/v1/user/owner/remove", "admin", iamEntity.AdminRole, nil)

	stored, ok := directory.snapshot("owner")
	require.True(t, ok)
	require.Equal(t, owner, stored)
	_, deleted := directory.mutations()
	require.Empty(t, deleted)
	suspended, revoked, cleared := iam.effects()
	require.Empty(t, suspended)
	require.Empty(t, revoked)
	require.Empty(t, cleared)
	require.Equal(t, http.StatusForbidden, suspendCode, suspendBody)
	require.Equal(t, http.StatusForbidden, deleteCode, deleteBody)
}
