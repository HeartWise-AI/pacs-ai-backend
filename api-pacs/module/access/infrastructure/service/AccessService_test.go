package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/domain/entity"
	domainRepository "api-pacs/module/access/domain/repository"
	iamEntity "api-pacs/module/iam/domain/entity"
)

type fakeRoleRepository struct {
	roles       map[string]entity.CustomRole
	users       map[string]domainRepository.UserRoleAssignment
	assignCalls int
	userReads   int
	roleReads   int
	failUsers   bool
	failRoles   bool
}

func newFakeRepository() *fakeRoleRepository {
	return &fakeRoleRepository{roles: map[string]entity.CustomRole{}, users: map[string]domainRepository.UserRoleAssignment{}}
}

func (repository *fakeRoleRepository) ListRoles(_ context.Context, tenantID string) ([]entity.CustomRole, error) {
	roles := []entity.CustomRole{}
	for _, role := range repository.roles {
		if role.TenantID == tenantID {
			roles = append(roles, role)
		}
	}
	return roles, nil
}

func (repository *fakeRoleRepository) GetRole(_ context.Context, tenantID, roleID string) (entity.CustomRole, error) {
	repository.roleReads++
	if repository.failRoles {
		return entity.CustomRole{}, errors.New(apiError.FirestoreError)
	}
	role, ok := repository.roles[roleID]
	if !ok || role.TenantID != tenantID {
		return entity.CustomRole{}, errors.New(apiError.MissingRecord)
	}
	return role, nil
}

func (repository *fakeRoleRepository) CreateRole(_ context.Context, role entity.CustomRole) error {
	if _, exists := repository.roles[role.ID]; exists {
		return errors.New(apiError.DuplicateRecord)
	}
	repository.roles[role.ID] = role
	return nil
}

func (repository *fakeRoleRepository) UpdateRole(_ context.Context, role entity.CustomRole) error {
	if existing, ok := repository.roles[role.ID]; !ok || existing.TenantID != role.TenantID {
		return errors.New(apiError.MissingRecord)
	}
	repository.roles[role.ID] = role
	return nil
}

// DeleteRole mirrors the transactional repository: it refuses while any user
// of the tenant still points at the role.
func (repository *fakeRoleRepository) DeleteRole(_ context.Context, tenantID string, roleID string) error {
	for _, user := range repository.users {
		if user.TenantID == tenantID && user.CustomRoleID == roleID {
			return errors.New(apiError.RoleInUse)
		}
	}
	delete(repository.roles, roleID)
	return nil
}

func (repository *fakeRoleRepository) GetUserAssignment(_ context.Context, tenantID, userID string) (domainRepository.UserRoleAssignment, error) {
	repository.userReads++
	if repository.failUsers {
		return domainRepository.UserRoleAssignment{}, errors.New(apiError.FirestoreError)
	}
	user, ok := repository.users[userID]
	if !ok || user.TenantID != tenantID {
		return domainRepository.UserRoleAssignment{}, errors.New(apiError.MissingRecord)
	}
	return user, nil
}

func (repository *fakeRoleRepository) ListUserAssignments(_ context.Context, tenantID string) ([]domainRepository.UserRoleAssignment, error) {
	users := []domainRepository.UserRoleAssignment{}
	for _, user := range repository.users {
		if user.TenantID == tenantID {
			users = append(users, user)
		}
	}
	return users, nil
}

func (repository *fakeRoleRepository) SetUserCustomRole(_ context.Context, _ string, userID, roleID string) error {
	repository.assignCalls++
	user := repository.users[userID]
	user.CustomRoleID = roleID
	repository.users[userID] = user
	return nil
}

func seeded() (*AccessService, *fakeRoleRepository, *time.Time) {
	repository := newFakeRepository()
	repository.roles["r1"] = entity.CustomRole{ID: "r1", TenantID: "t1", Name: "Readers", Permissions: []string{entity.PermFeedbackView}, ModelIDs: []string{"PanEcho"}}
	repository.roles["other"] = entity.CustomRole{ID: "other", TenantID: "t2", Name: "Other", Permissions: []string{entity.PermLogsView}, AllModels: true}
	repository.users["u1"] = domainRepository.UserRoleAssignment{UserID: "u1", TenantID: "t1", Role: iamEntity.UserRole, CustomRoleID: "r1"}
	repository.users["u2"] = domainRepository.UserRoleAssignment{UserID: "u2", TenantID: "t1", Role: iamEntity.UserRole}
	repository.users["a1"] = domainRepository.UserRoleAssignment{UserID: "a1", TenantID: "t1", Role: iamEntity.AdminRole}
	now := time.Unix(1_700_000_000, 0)
	service := &AccessService{Repository: repository, TTL: 30 * time.Second, Now: func() time.Time { return now }}
	return service, repository, &now
}

func TestResolveUsesStoredRoleAndNeverCachesAdmin(t *testing.T) {
	service, repository, _ := seeded()
	for range 2 {
		access, err := service.Resolve(context.Background(), "t1", "a1", iamEntity.AdminRole)
		require.NoError(t, err)
		require.True(t, access.Has(entity.PermWorkspaceManage))
		require.True(t, access.AllModels)
	}
	require.Equal(t, 2, repository.userReads)

	repository.users["a1"] = domainRepository.UserRoleAssignment{UserID: "a1", TenantID: "t1", Role: iamEntity.UserRole}
	access, err := service.Resolve(context.Background(), "t1", "a1", iamEntity.AdminRole)
	require.NoError(t, err)
	require.Equal(t, iamEntity.UserRole, access.Role)
	require.Equal(t, entity.BaselinePermissions, access.PermissionList())
}

func TestResolveUserWithCustomRole(t *testing.T) {
	service, _, _ := seeded()
	access, err := service.Resolve(context.Background(), "t1", "u1", iamEntity.UserRole)
	require.NoError(t, err)
	require.True(t, access.Has(entity.PermFeedbackView))
	require.True(t, access.Has(entity.PermModelsUse))
	require.False(t, access.Has(entity.PermMembersManage))
	require.True(t, access.AllowsModel("PanEcho"))
	require.False(t, access.AllowsModel("DeepRV_CLIP"))
	require.Equal(t, "Readers", access.CustomRole.Name)
}

func TestResolveUserWithoutRoleIsBaseline(t *testing.T) {
	service, _, _ := seeded()
	access, err := service.Resolve(context.Background(), "t1", "u2", iamEntity.UserRole)
	require.NoError(t, err)
	require.Equal(t, entity.BaselinePermissions, access.PermissionList())
	require.True(t, access.AllModels)
}

func TestResolveDanglingOrForeignRoleKeepsBaselineWithoutModels(t *testing.T) {
	service, repository, _ := seeded()
	for _, roleID := range []string{"deleted", "other"} {
		repository.users["u3"] = domainRepository.UserRoleAssignment{UserID: "u3", TenantID: "t1", Role: iamEntity.UserRole, CustomRoleID: roleID}
		service.InvalidateUser("t1", "u3")
		access, err := service.Resolve(context.Background(), "t1", "u3", iamEntity.UserRole)
		require.NoError(t, err, roleID)
		require.Equal(t, entity.BaselinePermissions, access.PermissionList(), roleID)
		require.False(t, access.AllModels, "a lost allowlist must not widen to every model (%s)", roleID)
		require.False(t, access.AllowsModel("PanEcho"), roleID)
		require.Nil(t, access.CustomRole, roleID)
	}
}

func TestUpdateRoleNeverRecreatesADeletedRole(t *testing.T) {
	service, repository, _ := seeded()
	repository.users["u1"] = domainRepository.UserRoleAssignment{UserID: "u1", TenantID: "t1", Role: iamEntity.UserRole}
	require.NoError(t, service.DeleteRole(context.Background(), "t1", "r1"))
	_, err := service.UpdateRole(context.Background(), "t1", "r1", RoleInput{Name: "Readers"})
	require.EqualError(t, err, apiError.MissingRecord)
	_, ok := repository.roles["r1"]
	require.False(t, ok)
}

func TestResolveFailsClosedOnStorageErrors(t *testing.T) {
	service, repository, _ := seeded()
	repository.failUsers = true
	_, err := service.Resolve(context.Background(), "t1", "u1", iamEntity.UserRole)
	require.EqualError(t, err, apiError.AccessUnavailable)

	repository.failUsers, repository.failRoles = false, true
	_, err = service.Resolve(context.Background(), "t1", "u1", iamEntity.UserRole)
	require.EqualError(t, err, apiError.AccessUnavailable)

	_, err = service.Resolve(context.Background(), "t1", "missing", iamEntity.UserRole)
	require.EqualError(t, err, apiError.AccessUnavailable)

	_, err = (&AccessService{}).Resolve(context.Background(), "t1", "u1", iamEntity.UserRole)
	require.EqualError(t, err, apiError.AccessUnavailable)
}

func TestResolveCachesUntilTTLOrInvalidation(t *testing.T) {
	service, repository, now := seeded()
	ctx := context.Background()
	_, err := service.Resolve(ctx, "t1", "u1", iamEntity.UserRole)
	require.NoError(t, err)
	_, err = service.Resolve(ctx, "t1", "u1", iamEntity.UserRole)
	require.NoError(t, err)
	require.Equal(t, 1, repository.userReads)

	*now = now.Add(31 * time.Second)
	_, err = service.Resolve(ctx, "t1", "u1", iamEntity.UserRole)
	require.NoError(t, err)
	require.Equal(t, 2, repository.userReads)

	// Editing the role takes effect at once for its members.
	_, err = service.UpdateRole(ctx, "t1", "r1", RoleInput{Name: "Readers", Permissions: []string{entity.PermLogsView}, AllModels: true})
	require.NoError(t, err)
	access, err := service.Resolve(ctx, "t1", "u1", iamEntity.UserRole)
	require.NoError(t, err)
	require.True(t, access.Has(entity.PermLogsView))
	require.False(t, access.Has(entity.PermFeedbackView))
	require.True(t, access.AllModels)

	// Unassigning takes effect at once for that user.
	require.NoError(t, service.AssignCustomRole(ctx, "t1", "u1", ""))
	access, err = service.Resolve(ctx, "t1", "u1", iamEntity.UserRole)
	require.NoError(t, err)
	require.False(t, access.Has(entity.PermLogsView))
}

func TestCreateRoleValidatesAndNormalizes(t *testing.T) {
	service, repository, _ := seeded()
	ctx := context.Background()
	view, err := service.CreateRole(ctx, "t1", "a1", RoleInput{
		Name:        "  Echo readers ",
		Description: " Echo only ",
		Permissions: []string{entity.PermModelsUse, entity.PermMembersInvite, entity.PermMembersInvite, entity.PermFeedbackView},
		ModelIDs:    []string{"PanEcho", " PanEcho", "TotalSegmentator"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, view.ID)
	require.Equal(t, "Echo readers", view.Name)
	require.Equal(t, "Echo only", view.Description)
	require.Equal(t, []string{entity.PermFeedbackView, entity.PermMembersInvite}, view.Permissions)
	require.Equal(t, []string{"PanEcho", "TotalSegmentator"}, view.ModelIDs)
	require.Equal(t, "t1", repository.roles[view.ID].TenantID)
	require.Equal(t, "a1", repository.roles[view.ID].CreatedBy)

	view, err = service.CreateRole(ctx, "t1", "a1", RoleInput{Name: "All", AllModels: true, ModelIDs: []string{"PanEcho"}})
	require.NoError(t, err)
	require.Empty(t, view.ModelIDs)
	require.NotNil(t, view.Permissions)

	for name, input := range map[string]RoleInput{
		"empty name":         {Name: "  "},
		"long name":          {Name: strings.Repeat("n", entity.MaxRoleNameLength+1)},
		"long description":   {Name: "ok", Description: strings.Repeat("d", entity.MaxRoleDescriptionLength+1)},
		"unknown permission": {Name: "ok", Permissions: []string{"roles.manage"}},
		"blank model":        {Name: "ok", ModelIDs: []string{" "}},
		"long model":         {Name: "ok", ModelIDs: []string{strings.Repeat("m", entity.MaxRoleModelIDLength+1)}},
	} {
		_, err := service.CreateRole(ctx, "t1", "a1", input)
		require.EqualError(t, err, apiError.InvalidPayload, name)
	}

	_, err = service.CreateRole(ctx, "t1", "a1", RoleInput{Name: "readers"})
	require.EqualError(t, err, apiError.DuplicateRecord, "names are unique per tenant, ignoring case")
	_, err = service.CreateRole(ctx, "t2", "a1", RoleInput{Name: "Readers"})
	require.NoError(t, err, "another tenant may reuse the name")
}

func TestUpdateRole(t *testing.T) {
	service, _, _ := seeded()
	ctx := context.Background()
	_, err := service.CreateRole(ctx, "t1", "a1", RoleInput{Name: "Second"})
	require.NoError(t, err)

	_, err = service.UpdateRole(ctx, "t1", "r1", RoleInput{Name: "SECOND"})
	require.EqualError(t, err, apiError.DuplicateRecord)
	_, err = service.UpdateRole(ctx, "t1", "other", RoleInput{Name: "x"})
	require.EqualError(t, err, apiError.MissingRecord, "roles of other tenants are invisible")

	view, err := service.UpdateRole(ctx, "t1", "r1", RoleInput{Name: "readers", Description: "renamed"})
	require.NoError(t, err)
	require.Equal(t, "readers", view.Name)
	require.Equal(t, 1, view.MemberCount)
}

func TestListRolesCountsUserMembersOnly(t *testing.T) {
	service, repository, _ := seeded()
	repository.users["a2"] = domainRepository.UserRoleAssignment{UserID: "a2", TenantID: "t1", Role: iamEntity.AdminRole, CustomRoleID: "r1"}
	views, err := service.ListRoles(context.Background(), "t1")
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, "r1", views[0].ID)
	require.Equal(t, 1, views[0].MemberCount)
}

func TestDeleteRoleRefusesWhileHeld(t *testing.T) {
	service, repository, _ := seeded()
	ctx := context.Background()
	require.EqualError(t, service.DeleteRole(ctx, "t1", "r1"), apiError.RoleInUse)
	require.EqualError(t, service.DeleteRole(ctx, "t1", "other"), apiError.MissingRecord)

	require.NoError(t, service.AssignCustomRole(ctx, "t1", "u1", ""))
	require.NoError(t, service.DeleteRole(ctx, "t1", "r1"))
	_, ok := repository.roles["r1"]
	require.False(t, ok)
}

func TestAssignCustomRoleRules(t *testing.T) {
	service, repository, _ := seeded()
	ctx := context.Background()
	require.EqualError(t, service.AssignCustomRole(ctx, "t1", "a1", "r1"), apiError.InvalidPayload, "admins hold every permission already")
	require.EqualError(t, service.AssignCustomRole(ctx, "t1", "u2", "missing"), apiError.MissingRecord)
	require.EqualError(t, service.AssignCustomRole(ctx, "t1", "u2", "other"), apiError.MissingRecord, "foreign tenant role")
	require.EqualError(t, service.AssignCustomRole(ctx, "t2", "u2", "r1"), apiError.MissingRecord, "foreign tenant user")
	require.Zero(t, repository.assignCalls)

	require.NoError(t, service.AssignCustomRole(ctx, "t1", "u2", " r1 "))
	require.Equal(t, "r1", repository.users["u2"].CustomRoleID)
	require.NoError(t, service.AssignCustomRole(ctx, "t1", "a1", ""), "clearing is always allowed")
}

func TestPermissionCatalog(t *testing.T) {
	catalog := PermissionCatalog()
	require.Len(t, catalog, len(entity.AllPermissions()))
	require.Equal(t, PermissionInfo{ID: entity.PermModelsUse, Baseline: true}, catalog[0])
	require.Equal(t, PermissionInfo{ID: entity.PermResultsViewAll, Baseline: false}, catalog[2])
}
