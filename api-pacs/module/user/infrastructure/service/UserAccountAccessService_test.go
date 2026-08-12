package service

import (
	"context"
	"errors"
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
	updates   []repositoryTypes.UpdateTenantUserAccessState
	deleted   string
	updateErr error
	sequence  *[]string
}

func (repository *accountAccessCommandRepository) UpdateTenantUserAccessState(_ context.Context, data repositoryTypes.UpdateTenantUserAccessState) error {
	if repository.updateErr != nil {
		return repository.updateErr
	}
	repository.updates = append(repository.updates, data)
	return nil
}

func (repository *accountAccessCommandRepository) DeleteTenantUser(_ context.Context, _, id string) error {
	*repository.sequence = append(*repository.sequence, "delete")
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
	setErr    error
	revokeErr error
	clearErr  error
	sequence  *[]string
}

func (iam *accountAccessIAM) SetUserSuspended(context.Context, string, string) error {
	iam.suspended++
	*iam.sequence = append(*iam.sequence, "suspend")
	return iam.setErr
}

func (iam *accountAccessIAM) RevokeUserSessions(context.Context, string, string) error {
	iam.revoked++
	*iam.sequence = append(*iam.sequence, "revoke")
	return iam.revokeErr
}

func (iam *accountAccessIAM) ClearUserSuspension(context.Context, string, string) error {
	iam.cleared++
	return iam.clearErr
}

func newAccountAccessService(user repositoryTypes.GetTenantUser) (*UserCommandService, *accountAccessCommandRepository, *accountAccessIAM) {
	sequence := []string{}
	commandRepository := &accountAccessCommandRepository{sequence: &sequence}
	iam := &accountAccessIAM{sequence: &sequence}
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
	for name, testCase := range map[string]struct {
		actorID, actorRole string
		target             repositoryTypes.GetTenantUser
	}{
		"admin cannot suspend owner": {"admin", iamEntity.AdminRole, repositoryTypes.GetTenantUser{ID: "owner", TenantID: "tenant-a", Role: iamEntity.OwnerRole, AccessState: entity.AccountAccessActive}},
		"admin cannot suspend self":  {"admin", iamEntity.AdminRole, repositoryTypes.GetTenantUser{ID: "admin", TenantID: "tenant-a", Role: iamEntity.AdminRole, AccessState: entity.AccountAccessActive}},
		"user cannot suspend user":   {"user-a", iamEntity.UserRole, repositoryTypes.GetTenantUser{ID: "user-b", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive}},
		"owner cannot suspend owner": {"owner-a", iamEntity.OwnerRole, repositoryTypes.GetTenantUser{ID: "owner-b", TenantID: "tenant-a", Role: iamEntity.OwnerRole, AccessState: entity.AccountAccessActive}},
	} {
		t.Run(name, func(t *testing.T) {
			service, commandRepository, iam := newAccountAccessService(testCase.target)
			err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
				TenantID: "tenant-a", ActorUserID: testCase.actorID, ActorRole: testCase.actorRole,
				TargetUserID: testCase.target.ID, AccessState: entity.AccountAccessSuspended,
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

	legacyService, legacyRepository, legacyIAM := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "legacy", TenantID: "tenant-a", Role: iamEntity.UserRole, IsAccountDisabled: true,
	})
	err = legacyService.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "legacy", AccessState: entity.AccountAccessSuspended,
	})
	require.NoError(t, err)
	require.Empty(t, legacyRepository.updates)
	require.Equal(t, 1, legacyIAM.suspended)
	require.Equal(t, 1, legacyIAM.revoked)
}

func TestChangeTenantUserAccessPropagatesFailClosedErrors(t *testing.T) {
	tests := []struct {
		name                               string
		state                              string
		configure                          func(*accountAccessCommandRepository, *accountAccessIAM)
		wantSuspend, wantRevoke, wantClear int
	}{
		{"suspension marker", entity.AccountAccessSuspended, func(_ *accountAccessCommandRepository, iam *accountAccessIAM) {
			iam.setErr = errors.New(apiError.DatabaseError)
		}, 1, 0, 0},
		{"session revocation", entity.AccountAccessSuspended, func(_ *accountAccessCommandRepository, iam *accountAccessIAM) {
			iam.revokeErr = errors.New(apiError.DatabaseError)
		}, 1, 1, 0},
		{"durable update", entity.AccountAccessSuspended, func(repository *accountAccessCommandRepository, _ *accountAccessIAM) {
			repository.updateErr = errors.New(apiError.FirestoreError)
		}, 1, 1, 1},
		{"reactivation marker clear", entity.AccountAccessActive, func(_ *accountAccessCommandRepository, iam *accountAccessIAM) {
			iam.clearErr = errors.New(apiError.DatabaseError)
		}, 0, 0, 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			initialState := entity.AccountAccessActive
			if testCase.state == entity.AccountAccessActive {
				initialState = entity.AccountAccessSuspended
			}
			service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
				ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: initialState,
			})
			testCase.configure(commandRepository, iam)
			err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
				TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
				TargetUserID: "target", AccessState: testCase.state,
			})
			require.Error(t, err)
			require.Equal(t, testCase.wantSuspend, iam.suspended)
			require.Equal(t, testCase.wantRevoke, iam.revoked)
			require.Equal(t, testCase.wantClear, iam.cleared)
		})
	}
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
	require.Equal(t, []string{"suspend", "revoke", "delete"}, *iam.sequence)
}

func TestAccessStateRejectsUnknownValue(t *testing.T) {
	service, _, _ := newAccountAccessService(repositoryTypes.GetTenantUser{})
	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{AccessState: "LOCKED"})
	require.EqualError(t, err, apiError.InvalidPayload)
}
