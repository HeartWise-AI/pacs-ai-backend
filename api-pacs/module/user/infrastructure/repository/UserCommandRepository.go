package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
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

// DeleteTenantUser delete tenant user for tenant
func (repository *UserCommandRepository) DeleteTenantUser(ctx context.Context, tenantID, id string) error {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
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

// InsertTenantUser creates a new tenant user for tenant
func (repository *UserCommandRepository) InsertTenantUser(ctx context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
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
		if strings.Contains(err.Error(), "already exist") {
			return "", errors.New(apiError.DuplicateRecord)
		}

		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	// create user in firestore
	user := entity.User{
		TenantID:  data.TenantID,
		Role:      data.Role,
		LicenseNo: data.LicenseNo,
		Specialty: data.Specialty,
		CreatedAt: int(time.Now().Unix()),
		UpdatedAt: int(time.Now().Unix()),
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

// UpdateTenantUser update tenant user for tenant
func (repository *UserCommandRepository) UpdateTenantUser(ctx context.Context, data repositoryTypes.UpdateTenantUser) error {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	params := (&auth.UserToUpdate{}).
		DisplayName(data.Name)

	_, err = tenantAuth.UpdateUser(ctx, data.ID, params)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	var user entity.User

	// update user in firestore
	updateTenantUser := []firestore.Update{
		{Path: "role", Value: data.Role},
		{Path: "license_no", Value: data.LicenseNo},
		{Path: "specialty", Value: data.Specialty},
		{Path: "updated_at", Value: int(time.Now().Unix())},
	}

	collectionPath := fmt.Sprintf("%s/%s", user.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateTenantUser)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateTenantUserPassword update tenant user password for tenant
// This also verifies the user email
func (repository *UserCommandRepository) UpdateTenantUserPassword(ctx context.Context, data repositoryTypes.UpdateTenantUserPassword) error {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	params := (&auth.UserToUpdate{}).
		Password(data.NewPassword).EmailVerified(true)

	_, err = tenantAuth.UpdateUser(ctx, data.ID, params)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	return nil
}
