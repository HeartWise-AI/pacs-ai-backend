package service

import (
	"context"
	"log"

	"github.com/segmentio/ksuid"

	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandService handles the User command service logic
type UserCommandService struct {
	repository.UserCommandRepositoryInterface
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

// TODO: ForgotTenantUserPassword

// TODO: UpdateTenantUser

// UpdateTenantUserPassword update user password
func (service *UserCommandService) UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error {
	err := service.UserCommandRepositoryInterface.UpdateTenantUserPassword(ctx, repositoryTypes.UpdateTenantUserPassword{
		TenantID:    data.TenantID,
		ID:          data.UID,
		NewPassword: data.NewPassword,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// TODO: VerifyTenantUserEmail

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
