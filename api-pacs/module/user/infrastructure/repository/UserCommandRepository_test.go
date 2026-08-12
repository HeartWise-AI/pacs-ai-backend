package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type fakeTenantUserCreator struct {
	userID                string
	createAuthErr         error
	createProfileErr      error
	deleteAuthErr         error
	createProfileHook     func()
	createdUser           repositoryTypes.CreateTenantUser
	profileUserID         string
	deletedUserID         string
	deleteContextCanceled bool
	createAuthCalls       int
	createProfileCalls    int
	deleteAuthCalls       int
}

func (creator *fakeTenantUserCreator) CreateAuthUser(_ context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	creator.createdUser = data
	creator.createAuthCalls++
	return creator.userID, creator.createAuthErr
}

func (creator *fakeTenantUserCreator) CreateProfile(_ context.Context, userID string, _ repositoryTypes.CreateTenantUser) error {
	creator.profileUserID = userID
	creator.createProfileCalls++
	if creator.createProfileHook != nil {
		creator.createProfileHook()
	}
	return creator.createProfileErr
}

func (creator *fakeTenantUserCreator) DeleteAuthUser(ctx context.Context, userID string) error {
	creator.deletedUserID = userID
	creator.deleteAuthCalls++
	creator.deleteContextCanceled = ctx.Err() != nil
	return creator.deleteAuthErr
}

func TestInsertTenantUserCreatesAuthIdentityAndProfile(t *testing.T) {
	creator := &fakeTenantUserCreator{userID: "firebase-user-id"}
	repository := &UserCommandRepository{userCreator: creator}
	data := validCreateTenantUser()

	userID, err := repository.InsertTenantUser(context.Background(), data)

	require.NoError(t, err)
	require.Equal(t, "firebase-user-id", userID)
	require.Equal(t, data, creator.createdUser)
	require.Equal(t, "firebase-user-id", creator.profileUserID)
	require.Equal(t, 1, creator.createAuthCalls)
	require.Equal(t, 1, creator.createProfileCalls)
	require.Equal(t, 0, creator.deleteAuthCalls)
}

func TestInsertTenantUserDeletesAuthIdentityWhenProfileCreationFails(t *testing.T) {
	requestContext, cancelRequest := context.WithCancel(context.Background())
	creator := &fakeTenantUserCreator{
		userID:            "firebase-user-id",
		createProfileErr:  errors.New("firestore unavailable"),
		createProfileHook: cancelRequest,
	}
	repository := &UserCommandRepository{userCreator: creator}

	userID, err := repository.InsertTenantUser(requestContext, validCreateTenantUser())

	require.Empty(t, userID)
	require.EqualError(t, err, apiError.FirestoreError)
	require.Equal(t, 1, creator.createAuthCalls)
	require.Equal(t, 1, creator.createProfileCalls)
	require.Equal(t, 1, creator.deleteAuthCalls)
	require.Equal(t, "firebase-user-id", creator.deletedUserID)
	require.False(t, creator.deleteContextCanceled, "rollback must outlive request cancellation")
}

func TestInsertTenantUserDoesNotCreateProfileWhenAuthCreationFails(t *testing.T) {
	creator := &fakeTenantUserCreator{createAuthErr: errors.New("firebase unavailable")}
	repository := &UserCommandRepository{userCreator: creator}

	userID, err := repository.InsertTenantUser(context.Background(), validCreateTenantUser())

	require.Empty(t, userID)
	require.EqualError(t, err, apiError.FirebaseAuthError)
	require.Equal(t, 1, creator.createAuthCalls)
	require.Equal(t, 0, creator.createProfileCalls)
	require.Equal(t, 0, creator.deleteAuthCalls)
}

func TestInsertTenantUserReturnsStableErrorWhenRollbackFails(t *testing.T) {
	creator := &fakeTenantUserCreator{
		userID:           "firebase-user-id",
		createProfileErr: errors.New("firestore unavailable"),
		deleteAuthErr:    errors.New("firebase unavailable"),
	}
	repository := &UserCommandRepository{userCreator: creator}

	userID, err := repository.InsertTenantUser(context.Background(), validCreateTenantUser())

	require.Empty(t, userID)
	require.EqualError(t, err, apiError.FirestoreError)
	require.Equal(t, 1, creator.deleteAuthCalls)
}

func validCreateTenantUser() repositoryTypes.CreateTenantUser {
	return repositoryTypes.CreateTenantUser{
		TenantID:        "tenant-a",
		Role:            "USER",
		Email:           "public.user@example.com",
		Password:        "ValidPassword!",
		Name:            "Public User",
		LicenseNo:       "demo-license",
		Specialty:       "demo-specialty",
		IsEmailVerified: false,
		IsAdminCreated:  false,
	}
}
