package service

import (
	"context"
	"log"

	"firebase.google.com/go/v4/auth"
	"github.com/segmentio/ksuid"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandService handles the User command service logic
type UserCommandService struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	repository.UserCommandRepositoryInterface
}

// AddTenantUser add a new tenant user with random generated password
func (service *UserCommandService) AddTenantUser(ctx context.Context, data types.AddTenantUser) (string, error) {
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

	id, err := service.UserCommandRepositoryInterface.InsertUser(ctx, repositoryTypes.CreateUser{
		TenantID:  user.TenantID,
		Role:      data.Role,
		Name:      data.Name,
		Email:     user.Email,
		Password:  generatedPassword,
		LicenseNo: data.LicenseNo,
		Specialty: data.Specialty,
	})
	if err != nil {
		return "", err
	}

	return id, nil
}

// DeleteTenantUser delete tenant user by id
func (service *UserCommandService) DeleteTenantUser(ctx context.Context, tenantID, uid string) error {
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
func (service *UserCommandService) UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error {
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
