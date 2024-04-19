package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"firebase.google.com/go/v4/auth"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

// UserCommandRepository handles the user command repository logic
type UserCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// DeleteUser delete user for tenant
func (repository *UserCommandRepository) DeleteUser(ctx context.Context, tenantID, id string) error {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// get firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete user in firebase auth
	err = tenantAuth.DeleteUser(ctx, id)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// delete user in firestore
	var user entity.User

	collectionPath := fmt.Sprintf("%s/%s", user.GetModelName(), id)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertUser creates a new user for tenant
func (repository *UserCommandRepository) InsertUser(ctx context.Context, data repositoryTypes.CreateUser) (string, error) {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	// get firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirestoreError)
	}

	// create user in firebase auth
	params := (&auth.UserToCreate{}).
		Email(data.Email).
		EmailVerified(false).
		Password(data.Password).
		DisplayName(data.Name).
		Disabled(false)

	authUser, err := tenantAuth.CreateUser(ctx, params)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirestoreError)
	}

	// create user in firestore
	user := entity.User{
		TenantID:  data.TenantID,
		Role:      data.Role,
		LicenseNo: data.LicenseNo,
		Specialty: data.Specialty,
		CreatedAt: uint(time.Now().Unix()),
		UpdatedAt: uint(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", user.GetModelName(), authUser.UID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, user)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirestoreError)
	}

	return authUser.UID, nil
}

// UpdateUserPassword update user password for tenant
func (repository *UserCommandRepository) UpdateUserPassword(ctx context.Context, data repositoryTypes.UpdateUserPassword) error {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	params := (&auth.UserToUpdate{}).
		Password(data.NewPassword)

	_, err = tenantAuth.UpdateUser(ctx, data.ID, params)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	return nil
}
