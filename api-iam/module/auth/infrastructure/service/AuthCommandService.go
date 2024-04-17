package service

import (
	"context"
	"log"

	"firebase.google.com/go/v4/auth"
	"github.com/segmentio/ksuid"

	"api-iam/infrastructures/providers/sdk/firebaseadmin"
	"api-iam/module/auth/infrastructure/service/types"
)

// AuthCommandService handles the Auth command service logic
type AuthCommandService struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// AddTenantUser add a new tenant user with random generated password
func (service *AuthCommandService) AddTenantUser(ctx context.Context, data types.AddTenantUser) (string, error) {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return "", err
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return "", err
	}

	// generate random password
	generatedPassword := generateID()

	params := (&auth.UserToCreate{}).
		Email(data.Email).
		EmailVerified(false).
		Password(generatedPassword).
		DisplayName(data.Name).
		Disabled(false)

	user, err := tenantAuth.CreateUser(ctx, params)
	if err != nil {
		log.Println(err)
		return "", err
	}

	// set claims
	claims := map[string]interface{}{
		types.TenantClaim: data.TenantID, // cannot be updated
		types.RoleClaim:   data.Role,     // can be updated
	}
	err = tenantAuth.SetCustomUserClaims(ctx, user.UID, claims)
	if err != nil {
		log.Println(err)
		return "", err
	}

	return generatedPassword, nil
}

// DeleteTenantUser delete tenant user by id
func (service *AuthCommandService) DeleteTenantUser(ctx context.Context, tenantID, uid string) error {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return err
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return err
	}

	err = tenantAuth.DeleteUser(ctx, uid)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// TODO: UpdateTenantUser

// UpdateTenantUserPassword update tenant user password
func (service *AuthCommandService) UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return err
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(data.TenantID)
	if err != nil {
		log.Println(err)
		return err
	}

	params := (&auth.UserToUpdate{}).
		Password(data.NewPassword)

	_, err = tenantAuth.UpdateUser(ctx, data.UID, params)
	if err != nil {
		return err
	}

	return nil
}

// TODO: VerifyTenantUserEmail

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
