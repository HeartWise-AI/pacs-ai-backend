package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type accountAccessCommandRepository struct {
	repository.UserCommandRepositoryInterface
	updates []repositoryTypes.UpdateTenantUserAccessState
	deleted string
}

func (repository *accountAccessCommandRepository) UpdateTenantUserAccessState(_ context.Context, data repositoryTypes.UpdateTenantUserAccessState) error {
	repository.updates = append(repository.updates, data)
	return nil
}

func (repository *accountAccessCommandRepository) DeleteTenantUser(_ context.Context, _, id string) error {
	repository.deleted = id
	return nil
}

type accountAccessQueryRepository struct {
	repository.UserQueryRepositoryInterface
	user repositoryTypes.GetTenantUser
}

func (repository *accountAccessQueryRepository) SelectTenantUserByID(context.Context, string, string) (repositoryTypes.GetTenantUser, error) {
	return repository.user, nil
}

type accountAccessIAM struct {
	iamApplication.IAMCommandServiceInterface
	suspended int
	revoked   int
	cleared   int
}

func (iam *accountAccessIAM) SetUserSuspended(context.Context, string, string) error {
	iam.suspended++
	return nil
}

func (iam *accountAccessIAM) RevokeUserSessions(context.Context, string, string) error {
	iam.revoked++
	return nil
}

func (iam *accountAccessIAM) ClearUserSuspension(context.Context, string, string) error {
	iam.cleared++
	return nil
}

func newAccountAccessService(user repositoryTypes.GetTenantUser) (*UserCommandService, *accountAccessCommandRepository, *accountAccessIAM) {
	commandRepository := &accountAccessCommandRepository{}
	iam := &accountAccessIAM{}
	return &UserCommandService{
		UserCommandRepositoryInterface: commandRepository,
		UserQueryRepositoryInterface:   &accountAccessQueryRepository{user: user},
		IAMCommandServiceInterface:     iam,
	}, commandRepository, iam
}

func TestOwnerSuspendsUserAndRevokesSessions(t *testing.T) {
	service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})

	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "target", AccessState: entity.AccountAccessSuspended, Reason: "abuse",
	})

	require.NoError(t, err)
	require.Equal(t, 1, iam.suspended)
	require.Equal(t, 1, iam.revoked)
	require.Equal(t, []repositoryTypes.UpdateTenantUserAccessState{{
		ID: "target", TenantID: "tenant-a", AccessState: entity.AccountAccessSuspended,
	}}, commandRepository.updates)
}

func TestAdminCannotSuspendOwnerOrSelf(t *testing.T) {
	for name, target := range map[string]repositoryTypes.GetTenantUser{
		"owner": {ID: "owner", TenantID: "tenant-a", Role: iamEntity.OwnerRole, AccessState: entity.AccountAccessActive},
		"self":  {ID: "admin", TenantID: "tenant-a", Role: iamEntity.AdminRole, AccessState: entity.AccountAccessActive},
	} {
		t.Run(name, func(t *testing.T) {
			service, commandRepository, iam := newAccountAccessService(target)
			err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
				TenantID: "tenant-a", ActorUserID: "admin", ActorRole: iamEntity.AdminRole,
				TargetUserID: target.ID, AccessState: entity.AccountAccessSuspended,
			})
			require.EqualError(t, err, apiError.ForbiddenAccess)
			require.Zero(t, iam.suspended)
			require.Empty(t, commandRepository.updates)
		})
	}
}

func TestSuspensionAndReactivationAreIdempotent(t *testing.T) {
	suspendedService, suspendedRepository, suspendedIAM := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessSuspended, IsAccountDisabled: true,
	})
	err := suspendedService.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
	})
	require.NoError(t, err)
	require.Empty(t, suspendedRepository.updates)
	require.Equal(t, 1, suspendedIAM.suspended)
	require.Equal(t, 1, suspendedIAM.revoked)

	activeService, activeRepository, activeIAM := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})
	err = activeService.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "target", AccessState: entity.AccountAccessActive,
	})
	require.NoError(t, err)
	require.Empty(t, activeRepository.updates)
	require.Equal(t, 1, activeIAM.cleared)
}

func TestDeleteRevokesSessionsBeforeRemovingUser(t *testing.T) {
	service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})

	err := service.DeleteTenantUser(context.Background(), serviceTypes.DeleteTenantUser{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole, TargetUserID: "target",
	})

	require.NoError(t, err)
	require.Equal(t, 1, iam.suspended)
	require.Equal(t, 1, iam.revoked)
	require.Equal(t, "target", commandRepository.deleted)
}

func TestAccessStateRejectsUnknownValue(t *testing.T) {
	service, _, _ := newAccountAccessService(repositoryTypes.GetTenantUser{})
	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{AccessState: "LOCKED"})
	require.EqualError(t, err, apiError.InvalidPayload)
}
