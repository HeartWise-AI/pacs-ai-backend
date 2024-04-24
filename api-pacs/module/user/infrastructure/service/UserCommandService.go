package service

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"

	"github.com/segmentio/ksuid"
)

// UserCommandService handles the User command service logic
type UserCommandService struct {
	repository.UserCommandRepositoryInterface
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// CreateTenantUser add a new tenant user with random generated password
func (service *UserCommandService) CreateTenantUser(ctx context.Context, data types.CreateTenantUser) (string, error) {
	// generate random password
	generatedPassword := generateID()

	_, err := service.UserCommandRepositoryInterface.InsertTenantUser(ctx, repositoryTypes.CreateTenantUser{
		TenantID:  data.TenantID,
		Role:      data.Role,
		Name:      data.Name,
		Email:     data.Email,
		Password:  generatedPassword,
		LicenseNo: data.LicenseNo,
		Specialty: data.Specialty,
	})
	if err != nil {
		return "", err
	}

	return generatedPassword, nil
}

// DeleteTenantUser delete tenant user by id
func (service *UserCommandService) DeleteTenantUser(ctx context.Context, tenantID, id string) error {
	err := service.UserCommandRepositoryInterface.DeleteTenantUser(ctx, tenantID, id)
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// ForgotTenantUserPassword sends a code by email to reset password
func (service *UserCommandService) ForgotTenantUserPassword(ctx context.Context, email string) error {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	firebaseAuth.PasswordResetLink(ctx, email)

	return nil
}

// UpdateTenantUser update tenant user
func (service *UserCommandService) UpdateTenantUser(ctx context.Context, data types.UpdateTenantUser) error {
	err := service.UserCommandRepositoryInterface.UpdateTenantUser(ctx, repositoryTypes.UpdateTenantUser{
		ID:        data.ID,
		TenantID:  data.TenantID,
		Role:      data.Role,
		Name:      data.Name,
		LicenseNo: data.LicenseNo,
		Specialty: data.Specialty,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// UpdateTenantUserPassword update user password
func (service *UserCommandService) UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error {
	err := service.UserCommandRepositoryInterface.UpdateTenantUserPassword(ctx, repositoryTypes.UpdateTenantUserPassword{
		ID:          data.ID,
		TenantID:    data.TenantID,
		NewPassword: data.NewPassword,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// VerifyTenantUserEmail verifies tenant user email
func (service *UserCommandService) VerifyTenantUserEmail(ctx context.Context, email string) error {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	firebaseAuth.EmailVerificationLink(ctx, email)

	return nil
}

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
