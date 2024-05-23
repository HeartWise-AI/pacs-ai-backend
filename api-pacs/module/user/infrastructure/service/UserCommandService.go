package service

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/segmentio/ksuid"

	awsSDKTypes "api-pacs/infrastructures/providers/sdk/aws/types"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/entity"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandService handles the User command service logic
type UserCommandService struct {
	repository.UserCommandRepositoryInterface
	userApplication.UserQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	awsSDKTypes.AWSSDKInterface
}

// CreateTenantUser add a new tenant user with random generated password
func (service *UserCommandService) CreateTenantUser(ctx context.Context, data types.CreateTenantUser) (string, error) {
	// generate random password
	generatedPassword := generateID()

	uid, err := service.UserCommandRepositoryInterface.InsertTenantUser(ctx, repositoryTypes.CreateTenantUser{
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

	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, uid)
	if err != nil {
		return "", err
	}

	go func() {
		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   data.TenantID,
			TenantName: user.Name,
			UserID:     uid,
			Email:      data.Email,
			Name:       data.Name,
			Role:       data.Role,
			LicenseNo:  data.LicenseNo,
			Specialty:  data.Specialty,
			Action:     entity.CreateAction,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	go func() {
		//  redirect link
		redirectLink := fmt.Sprintf("%s/%s/login", os.Getenv("APP_URL"), data.TenantID)

		// send to email
		emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
			"Here is your new PACS AI account credentials:<br /><br />"+
			"Email: %s <br />"+
			"Password: %s <br /><br />"+
			"You can use this and login to PACS AI via <a href=\"%s\">%s</a>. You will be then prompted to change password. <br /><br />"+
			"Thanks, <br /><br />"+
			"Your PACS AI team", data.Name, data.Email, generatedPassword, redirectLink, redirectLink)
		err = service.AWSSDKInterface.SESSendEmail(ctx, awsSDKTypes.SESSendEmailRequest{
			Subject:          "[PACS AI]: New account credentials",
			ToAddresses:      []string{data.Email},
			PlainTextMessage: emailMessage,
		})
		if err != nil {
			log.Println("[error] cannot send account credentials via aws ses", err)
			return
		}
	}()

	return generatedPassword, nil
}

// DeleteTenantUser delete tenant user by id
func (service *UserCommandService) DeleteTenantUser(ctx context.Context, tenantID, id string) error {
	err := service.UserCommandRepositoryInterface.DeleteTenantUser(ctx, tenantID, id)
	if err != nil {
		log.Println(err)
		return err
	}

	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, id)
	if err != nil {
		return err
	}

	go func() {
		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   user.TenantID,
			TenantName: user.Name,
			UserID:     user.ID,
			Email:      user.Email,
			Name:       user.Name,
			Role:       user.Role,
			LicenseNo:  user.LicenseNo,
			Specialty:  user.Specialty,
			Action:     entity.DeleteAction,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

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

	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, data.TenantID, data.ID)
	if err != nil {
		return err
	}

	go func() {
		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   user.TenantID,
			TenantName: user.Name,
			UserID:     user.ID,
			Email:      user.Email,
			Name:       user.Name,
			Role:       user.Role,
			LicenseNo:  user.LicenseNo,
			Specialty:  user.Specialty,
			Action:     entity.UpdateAction,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

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

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
