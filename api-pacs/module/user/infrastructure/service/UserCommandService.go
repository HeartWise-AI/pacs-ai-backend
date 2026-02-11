package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/segmentio/ksuid"

	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/entity"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandService handles the User command service logic
type UserCommandService struct {
	repository.UserCommandRepositoryInterface
	repository.UserQueryRepositoryInterface
	userApplication.UserQueryServiceInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	mailgunTypes.MailgunSDKInterface
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

	// logs to elasticsearch
	go func() {
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   data.TenantID,
			TenantName: tenant.Name,
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

	// log to elasticsearch
	go func() {
		//  redirect link
		redirectLink := fmt.Sprintf("%s/login?t=%s", os.Getenv("APP_URL"), data.TenantID)

		// send to email
		emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
			"Here is your new PACS AI account credentials:<br /><br />"+
			"Email: %s <br />"+
			"Password: %s <br /><br />"+
			"You can use this and login to PACS AI via <a href=\"%s\">%s</a>. You will be then prompted to change password. <br /><br />"+
			"Thanks, <br /><br />"+
			"Your PACS AI team", data.Name, data.Email, generatedPassword, redirectLink, redirectLink)
		err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
			Subject:       "[PACS AI]: New account credentials",
			Recipient:     data.Email,
			PlainTextBody: emailMessage,
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
	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, id)
	if err != nil {
		return err
	}

	err = service.UserCommandRepositoryInterface.DeleteTenantUser(ctx, tenantID, id)
	if err != nil {
		log.Println(err)
		return err
	}

	// log to elasticsearch
	go func() {
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   user.TenantID,
			TenantName: tenant.Name,
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

	// log to elasticsearch
	go func() {
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateAdminMemberLog(ctx, elasticsearchTypes.CreateAdminMemberLog{
			TenantID:   user.TenantID,
			TenantName: tenant.Name,
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

// UpdateUserMetadata update user metadata
func (service *UserCommandService) UpdateUserMetadata(ctx context.Context, data types.UpdateUserMetadata) error {
	metadataJSON, err := json.Marshal(data.Metadata)
	if err != nil {
		return err
	}

	err = service.UserCommandRepositoryInterface.UpsertUserMetadata(ctx, repositoryTypes.UpsertUserMetadata{
		UserID:   data.UserID,
		Metadata: string(metadataJSON),
	})
	if err != nil {
		return err
	}

	return nil
}

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
