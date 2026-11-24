package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	"api-pacs/module/user/domain/entity"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

func TestAuthorizeAccountManagementWithMembersManage(t *testing.T) {
	require.NoError(t, authorizeAccountManagement("user-a", iamEntity.UserRole, "user-b", iamEntity.UserRole, true))
	require.EqualError(t, authorizeAccountManagement("user-a", iamEntity.UserRole, "user-b", iamEntity.UserRole, false), apiError.ForbiddenAccess)
	require.EqualError(t, authorizeAccountManagement("user-a", iamEntity.UserRole, "admin", iamEntity.AdminRole, true), apiError.ForbiddenAccess)
	require.EqualError(t, authorizeAccountManagement("user-a", iamEntity.UserRole, "owner", iamEntity.OwnerRole, true), apiError.ForbiddenAccess)
	require.EqualError(t, authorizeAccountManagement("user-a", iamEntity.UserRole, "user-a", iamEntity.UserRole, true), apiError.ForbiddenAccess)
	// Existing rules are unchanged.
	require.NoError(t, authorizeAccountManagement("admin", iamEntity.AdminRole, "user-b", iamEntity.UserRole, false))
	require.EqualError(t, authorizeAccountManagement("admin", iamEntity.AdminRole, "owner", iamEntity.OwnerRole, true), apiError.ForbiddenAccess)
}

func TestUserWithMembersManageSuspendsAnotherUser(t *testing.T) {
	service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "user-b", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})
	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "user-a", ActorRole: iamEntity.UserRole,
		TargetUserID: "user-b", AccessState: entity.AccountAccessSuspended, ActorCanManageMembers: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, iam.suspended)
	require.Len(t, commandRepository.updates, 1)
}

func TestUserWithMembersManageCannotSuspendAdmin(t *testing.T) {
	service, commandRepository, _ := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "admin-b", TenantID: "tenant-a", Role: iamEntity.AdminRole, AccessState: entity.AccountAccessActive,
	})
	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "user-a", ActorRole: iamEntity.UserRole,
		TargetUserID: "admin-b", AccessState: entity.AccountAccessSuspended, ActorCanManageMembers: true,
	})
	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.Empty(t, commandRepository.updates)
}

type missingTenantQuery struct {
	tenantApplication.TenantQueryServiceInterface
}

func (missingTenantQuery) GetTenantByID(context.Context, string) (tenantTypes.GetTenantResult, error) {
	return tenantTypes.GetTenantResult{}, errors.New(apiError.MissingRecord)
}

type updateRecordingRepository struct {
	accountAccessCommandRepository
	updated []repositoryTypes.UpdateTenantUser
}

func (repository *updateRecordingRepository) UpdateTenantUser(_ context.Context, data repositoryTypes.UpdateTenantUser) error {
	repository.updated = append(repository.updated, data)
	return nil
}

func TestUpdateTenantUserByUserActorKeepsRolesUnchanged(t *testing.T) {
	users := map[string]repositoryTypes.GetTenantUser{
		"user-a": {ID: "user-a", TenantID: "tenant-a", Role: iamEntity.UserRole},
		"user-b": {ID: "user-b", TenantID: "tenant-a", Role: iamEntity.UserRole},
		"admin":  {ID: "admin", TenantID: "tenant-a", Role: iamEntity.AdminRole},
	}
	for name, testCase := range map[string]struct {
		target, role string
		allowed      bool
	}{
		"edit another user's details": {"user-b", iamEntity.UserRole, true},
		"promote a user":              {"user-b", iamEntity.AdminRole, false},
		"edit an admin":               {"admin", iamEntity.AdminRole, false},
		"demote an admin":             {"admin", iamEntity.UserRole, false},
		"edit self":                   {"user-a", iamEntity.UserRole, false},
		"unknown target":              {"ghost", iamEntity.UserRole, false},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &updateRecordingRepository{}
			service := &UserCommandService{
				UserCommandRepositoryInterface: repository,
				UserQueryRepositoryInterface:   &accountAccessQueryRepository{users: users},
				TenantQueryServiceInterface:    missingTenantQuery{},
			}
			err := service.UpdateTenantUser(context.Background(), serviceTypes.UpdateTenantUser{
				ID: testCase.target, TenantID: "tenant-a", Role: testCase.role, Name: "n",
				ActorUserID: "user-a", ActorRole: iamEntity.UserRole,
			})
			if testCase.allowed {
				require.NoError(t, err)
				require.Len(t, repository.updated, 1)
				return
			}
			require.Error(t, err)
			require.Empty(t, repository.updated)
		})
	}
}

type inviteQueryRepository struct {
	accountAccessQueryRepository
	invites map[string]entity.UserEmailInvite
}

func (repository *inviteQueryRepository) SelectTenantUserEmailInviteByID(_ context.Context, tenantID, id string) (entity.UserEmailInvite, error) {
	invite, ok := repository.invites[id]
	if !ok || invite.TenantID != tenantID {
		return entity.UserEmailInvite{}, errors.New(apiError.MissingRecord)
	}
	return invite, nil
}

type inviteDeletingRepository struct {
	accountAccessCommandRepository
	deletedInvites []string
}

func (repository *inviteDeletingRepository) DeleteTenantUserEmailInvite(_ context.Context, id string) error {
	repository.deletedInvites = append(repository.deletedInvites, id)
	return nil
}

func TestDeleteEmailInviteIsTenantScoped(t *testing.T) {
	command := &inviteDeletingRepository{}
	service := &UserCommandService{
		UserCommandRepositoryInterface: command,
		UserQueryRepositoryInterface: &inviteQueryRepository{invites: map[string]entity.UserEmailInvite{
			"inv-a": {ID: "inv-a", TenantID: "tenant-a"}, "inv-b": {ID: "inv-b", TenantID: "tenant-b"},
		}},
	}
	require.EqualError(t, service.DeleteTenantUserEmailInvite(context.Background(), "tenant-a", "inv-b"), apiError.MissingRecord)
	require.Empty(t, command.deletedInvites)
	require.NoError(t, service.DeleteTenantUserEmailInvite(context.Background(), "tenant-a", "inv-a"))
	require.Equal(t, []string{"inv-a"}, command.deletedInvites)
}
