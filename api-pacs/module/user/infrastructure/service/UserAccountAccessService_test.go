package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
	deleteErr error
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
	if repository.deleteErr != nil {
		return repository.deleteErr
	}
	repository.deleted = id
	return nil
}

type accountAccessQueryRepository struct {
	repository.UserQueryRepositoryInterface
	users map[string]repositoryTypes.GetTenantUser
}

func (repository *accountAccessQueryRepository) SelectTenantUserByID(_ context.Context, _ string, id string) (repositoryTypes.GetTenantUser, error) {
	user, ok := repository.users[id]
	if !ok {
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}
	return user, nil
}

type accountAccessIAM struct {
	iamApplication.IAMCommandServiceInterface
	suspended       int
	revoked         int
	cleared         int
	setErr          error
	markerCreated   bool
	revokeErr       error
	clearErr        error
	sequence        *[]string
	mu              sync.Mutex
	lockHeld        bool
	lockToken       string
	acquired        int
	released        int
	suspendDeadline time.Time
	suspendStarted  chan struct{}
	continueSuspend chan struct{}
}

func (iam *accountAccessIAM) AcquireUserAccessTransition(_ context.Context, _, _, ownerToken string, _ time.Duration) (bool, error) {
	iam.mu.Lock()
	defer iam.mu.Unlock()
	iam.acquired++
	if iam.lockHeld {
		return false, nil
	}
	iam.lockHeld = true
	iam.lockToken = ownerToken
	return true, nil
}

func (iam *accountAccessIAM) ReleaseUserAccessTransition(_ context.Context, _, _, ownerToken string) error {
	iam.mu.Lock()
	defer iam.mu.Unlock()
	if iam.lockHeld && iam.lockToken == ownerToken {
		iam.lockHeld = false
		iam.released++
	}
	return nil
}

func (iam *accountAccessIAM) SetUserSuspended(ctx context.Context, _, _ string) (bool, error) {
	iam.suspended++
	*iam.sequence = append(*iam.sequence, "suspend")
	iam.suspendDeadline, _ = ctx.Deadline()
	if iam.suspendStarted != nil {
		close(iam.suspendStarted)
		<-iam.continueSuspend
	}
	return iam.markerCreated, iam.setErr
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
	iam := &accountAccessIAM{sequence: &sequence, markerCreated: true}
	users := map[string]repositoryTypes.GetTenantUser{
		user.ID:   user,
		"owner":   {ID: "owner", TenantID: user.TenantID, Role: iamEntity.OwnerRole, AccessState: entity.AccountAccessActive},
		"owner-a": {ID: "owner-a", TenantID: user.TenantID, Role: iamEntity.OwnerRole, AccessState: entity.AccountAccessActive},
		"admin":   {ID: "admin", TenantID: user.TenantID, Role: iamEntity.AdminRole, AccessState: entity.AccountAccessActive},
		"user-a":  {ID: "user-a", TenantID: user.TenantID, Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive},
	}
	users[user.ID] = user
	return &UserCommandService{
		UserCommandRepositoryInterface: commandRepository,
		UserQueryRepositoryInterface:   &accountAccessQueryRepository{users: users},
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
		}, 1, 1, 1},
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

func TestFailedSuspensionPreservesExistingMarker(t *testing.T) {
	service, _, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole,
		AccessState: entity.AccountAccessSuspended, IsAccountDisabled: true,
	})
	iam.revokeErr = errors.New(apiError.DatabaseError)

	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.Zero(t, iam.cleared)
}

func TestFailedSuspensionDoesNotClearMarkerCreatedByAnotherOperation(t *testing.T) {
	service, _, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})
	iam.markerCreated = false
	iam.revokeErr = errors.New(apiError.DatabaseError)

	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.Zero(t, iam.cleared)
}

func TestUsesCurrentStoredActorRoleInsteadOfCachedSessionRole(t *testing.T) {
	service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})
	query := service.UserQueryRepositoryInterface.(*accountAccessQueryRepository)
	query.users["demoted-admin"] = repositoryTypes.GetTenantUser{
		ID: "demoted-admin", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	}

	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "demoted-admin", ActorRole: iamEntity.AdminRole,
		TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
	})

	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.Zero(t, iam.suspended)
	require.Empty(t, commandRepository.updates)
}

func TestSuspendedActorCannotManageTenantUsers(t *testing.T) {
	actors := []struct {
		name string
		user repositoryTypes.GetTenantUser
	}{
		{
			name: "suspended access state",
			user: repositoryTypes.GetTenantUser{
				ID: "suspended-owner", TenantID: "tenant-a", Role: iamEntity.OwnerRole,
				AccessState: entity.AccountAccessSuspended,
			},
		},
		{
			name: "legacy Firebase disabled state",
			user: repositoryTypes.GetTenantUser{
				ID: "suspended-owner", TenantID: "tenant-a", Role: iamEntity.OwnerRole,
				IsAccountDisabled: true,
			},
		},
	}
	tests := []struct {
		name string
		act  func(*UserCommandService) error
	}{
		{
			name: "suspend",
			act: func(service *UserCommandService) error {
				return service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
					TenantID: "tenant-a", ActorUserID: "suspended-owner", ActorRole: iamEntity.OwnerRole,
					TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
				})
			},
		},
		{
			name: "reactivate",
			act: func(service *UserCommandService) error {
				return service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
					TenantID: "tenant-a", ActorUserID: "suspended-owner", ActorRole: iamEntity.OwnerRole,
					TargetUserID: "target", AccessState: entity.AccountAccessActive,
				})
			},
		},
		{
			name: "delete",
			act: func(service *UserCommandService) error {
				return service.DeleteTenantUser(context.Background(), serviceTypes.DeleteTenantUser{
					TenantID: "tenant-a", ActorUserID: "suspended-owner", ActorRole: iamEntity.OwnerRole,
					TargetUserID: "target",
				})
			},
		},
	}

	for _, actor := range actors {
		t.Run(actor.name, func(t *testing.T) {
			for _, testCase := range tests {
				t.Run(testCase.name, func(t *testing.T) {
					service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
						ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessSuspended,
					})
					query := service.UserQueryRepositoryInterface.(*accountAccessQueryRepository)
					query.users["suspended-owner"] = actor.user

					err := testCase.act(service)

					require.EqualError(t, err, apiError.ForbiddenAccess)
					require.Zero(t, iam.suspended)
					require.Zero(t, iam.revoked)
					require.Zero(t, iam.cleared)
					require.Empty(t, commandRepository.updates)
					require.Empty(t, commandRepository.deleted)
				})
			}
		})
	}
}

func TestAccountAccessTransitionCriticalOperationsUseDeadlineBelowLockTTL(t *testing.T) {
	tests := []struct {
		name string
		act  func(*UserCommandService) error
	}{
		{
			name: "access state change",
			act: func(service *UserCommandService) error {
				return service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
					TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
					TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
				})
			},
		},
		{
			name: "delete",
			act: func(service *UserCommandService) error {
				return service.DeleteTenantUser(context.Background(), serviceTypes.DeleteTenantUser{
					TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
					TargetUserID: "target",
				})
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service, _, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
				ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
			})
			startedAt := time.Now()

			err := testCase.act(service)

			require.NoError(t, err)
			require.False(t, iam.suspendDeadline.IsZero())
			require.WithinDuration(t, startedAt.Add(userAccessTransitionOperationTimeout), iam.suspendDeadline, time.Second)
			require.Less(t, userAccessTransitionOperationTimeout, userAccessTransitionLockTTL)
			require.Less(t, iam.suspendDeadline.Sub(startedAt), userAccessTransitionLockTTL)
		})
	}
}

func TestDeleteUsesCurrentStoredActorRoleInsteadOfCachedSessionRole(t *testing.T) {
	service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})
	query := service.UserQueryRepositoryInterface.(*accountAccessQueryRepository)
	query.users["demoted-admin"] = repositoryTypes.GetTenantUser{
		ID: "demoted-admin", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	}

	err := service.DeleteTenantUser(context.Background(), serviceTypes.DeleteTenantUser{
		TenantID: "tenant-a", ActorUserID: "demoted-admin", ActorRole: iamEntity.AdminRole, TargetUserID: "target",
	})

	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.Zero(t, iam.suspended)
	require.Empty(t, commandRepository.deleted)
}

func TestConcurrentAccountAccessTransitionsAreSerialized(t *testing.T) {
	service, _, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
	})
	iam.suspendStarted = make(chan struct{})
	iam.continueSuspend = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
			TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
			TargetUserID: "target", AccessState: entity.AccountAccessSuspended,
		})
	}()
	<-iam.suspendStarted

	secondErr := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole,
		TargetUserID: "target", AccessState: entity.AccountAccessActive,
	})
	close(iam.continueSuspend)

	require.EqualError(t, secondErr, apiError.AccountAccessTransitionInProgress)
	require.NoError(t, <-firstDone)
	require.Equal(t, 2, iam.acquired)
	require.Equal(t, 1, iam.released)
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

func TestDeleteFailureClearsTemporarySuspensionMarker(t *testing.T) {
	for name, configure := range map[string]func(*accountAccessCommandRepository, *accountAccessIAM){
		"revocation failure": func(_ *accountAccessCommandRepository, iam *accountAccessIAM) {
			iam.revokeErr = errors.New(apiError.DatabaseError)
		},
		"repository failure": func(repository *accountAccessCommandRepository, _ *accountAccessIAM) {
			repository.deleteErr = errors.New(apiError.FirestoreError)
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
				ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole, AccessState: entity.AccountAccessActive,
			})
			configure(commandRepository, iam)
			err := service.DeleteTenantUser(context.Background(), serviceTypes.DeleteTenantUser{
				TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole, TargetUserID: "target",
			})
			require.Error(t, err)
			require.Equal(t, 1, iam.cleared)
			require.Empty(t, commandRepository.deleted)
		})
	}
}

func TestDeleteFailurePreservesExistingSuspensionMarker(t *testing.T) {
	service, commandRepository, iam := newAccountAccessService(repositoryTypes.GetTenantUser{
		ID: "target", TenantID: "tenant-a", Role: iamEntity.UserRole,
		AccessState: entity.AccountAccessSuspended, IsAccountDisabled: true,
	})
	commandRepository.deleteErr = errors.New(apiError.FirestoreError)

	err := service.DeleteTenantUser(context.Background(), serviceTypes.DeleteTenantUser{
		TenantID: "tenant-a", ActorUserID: "owner", ActorRole: iamEntity.OwnerRole, TargetUserID: "target",
	})

	require.EqualError(t, err, apiError.FirestoreError)
	require.Zero(t, iam.cleared)
}

func TestAccessStateRejectsUnknownValue(t *testing.T) {
	service, _, _ := newAccountAccessService(repositoryTypes.GetTenantUser{})
	err := service.ChangeTenantUserAccess(context.Background(), serviceTypes.ChangeTenantUserAccess{AccessState: "LOCKED"})
	require.EqualError(t, err, apiError.InvalidPayload)
}
