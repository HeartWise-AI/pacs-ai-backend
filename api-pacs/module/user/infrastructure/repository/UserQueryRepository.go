package repository

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

// UserQueryRepository handles the user query repository logic
type UserQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectTenantUserByID get tenant user by id
func (repository *UserQueryRepository) SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error) {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	// get firebase auth user
	authUser, err := tenantAuth.GetUser(ctx, id)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// get firestore user
	var user entity.User
	var firestoreUser repositoryTypes.GetTenantUser

	firestoreRes, err := firestoreClient.Collection(user.GetModelName()).Doc(id).Get(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&firestoreUser)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	return repositoryTypes.GetTenantUser{
		ID:                authUser.UID,
		TenantID:          authUser.TenantID,
		Role:              firestoreUser.Role,
		Name:              authUser.DisplayName,
		Email:             authUser.Email,
		LicenseNo:         firestoreUser.LicenseNo,
		Specialty:         firestoreUser.Specialty,
		IsEmailVerified:   authUser.EmailVerified,
		IsAccountDisabled: authUser.Disabled,
		CreatedAt:         firestoreUser.CreatedAt,
		UpdatedAt:         firestoreUser.UpdatedAt,
	}, nil
}

// TODO: implement get users logic from firebase auth and firestore
// SelectUsersByTenant get users by tenant id
// func (repository *UserQueryRepository) SelectUsersByTenant(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error) {
// 	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
// 	if err != nil {
// 		log.Println(err)
// 		return []repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
// 	}

// 	// tenant auth
// 	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
// 	if err != nil {
// 		log.Println(err)
// 		return []repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
// 	}

// 	// firestore client
// 	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
// 	if err != nil {
// 		log.Println(err)
// 		return []repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
// 	}

// }
