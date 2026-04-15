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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// DeleteTenantUserEmailInvite deletes a tenant user email invite
func (repository *UserCommandRepository) DeleteTenantUserEmailInvite(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	var emailInvite entity.UserEmailInvite

	collectionPath := fmt.Sprintf("%s/%s", emailInvite.GetModelName(), ID)
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

// InsertTenantUserEmailInvite creates a new tenant user email invite
func (repository *UserCommandRepository) InsertTenantUserEmailInvite(ctx context.Context, data repositoryTypes.CreateTenantUserEmailInvite) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// create user email invite in firestore
	emailInvite := entity.UserEmailInvite{
		ID:        data.ID,
		TenantID:  data.TenantID,
		Code:      data.Code,
		Email:     data.Email,
		ExpiresAt: int(data.ExpiresAt.Unix()),
		CreatedAt: int(time.Now().Unix()),
		UpdatedAt: int(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", emailInvite.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, emailInvite)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
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

// UpdateTenantUserConsent update tenant user consent for tenant
func (repository *UserCommandRepository) UpdateTenantUserConsent(ctx context.Context, data repositoryTypes.UpdateTenantUserConsent) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	var user entity.User

	// update user in firestore
	updateTenantUser := []firestore.Update{
		{Path: "is_consent_signed", Value: data.IsConsentSigned},
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

// UpdateTenantUserEmailInvite update tenant user email invite
func (repository *UserCommandRepository) UpdateTenantUserEmailInvite(ctx context.Context, data repositoryTypes.UpdateTenantUserEmailInvite) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	var emailInvite entity.UserEmailInvite

	// update user email invite in firestore
	updateEmailInvite := []firestore.Update{
		{Path: "code", Value: data.Code},
		{Path: "expires_at", Value: int(data.ExpiresAt.Unix())},
		{Path: "updated_at", Value: int(time.Now().Unix())},
	}

	collectionPath := fmt.Sprintf("%s/%s", emailInvite.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateEmailInvite)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateTenantUserEmailInviteVerifiedAt update tenant user email invite verified at
func (repository *UserCommandRepository) UpdateTenantUserEmailInviteVerifiedAt(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	var emailInvite entity.UserEmailInvite

	// update user email invite in firestore
	updateEmailInvite := []firestore.Update{
		{Path: "verified_at", Value: int(time.Now().Unix())},
	}

	collectionPath := fmt.Sprintf("%s/%s", emailInvite.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateEmailInvite)
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

// UpsertUserMetadata upserts user metadata
func (repository *UserCommandRepository) UpsertUserMetadata(ctx context.Context, data repositoryTypes.UpsertUserMetadata) error {
	var userMetadata entity.UserMetadata

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	collectionPath := fmt.Sprintf("%s/%s", userMetadata.GetModelName(), data.UserID)
	docRef := firestoreClient.Doc(collectionPath)

	// try to insert user metadata
	_, err = docRef.Create(ctx, entity.UserMetadata{
		UserID:    data.UserID,
		Metadata:  data.Metadata,
		CreatedAt: int(time.Now().Unix()),
		UpdatedAt: int(time.Now().Unix()),
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			// update user metadata
			updateUserMetadata := []firestore.Update{
				{
					Path:  "metadata",
					Value: data.Metadata,
				},
				{
					Path:  "updated_at",
					Value: int(time.Now().Unix()),
				},
			}

			_, err = docRef.Update(ctx, updateUserMetadata)
			if err != nil {
				log.Println(err)
				return errors.New(apiError.FirestoreError)
			}

			return nil
		}

		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}
