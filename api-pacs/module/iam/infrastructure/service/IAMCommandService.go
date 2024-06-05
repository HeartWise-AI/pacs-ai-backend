package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/segmentio/ksuid"

	awsSDKTypes "api-pacs/infrastructures/providers/sdk/aws/types"
	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/iam/domain/repository"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	tenantApplication "api-pacs/module/tenant/application"
	userApplication "api-pacs/module/user/application"
)

// IAMCommandService handles the IAM command service logic
type IAMCommandService struct {
	repository.IAMCommandRepositoryInterface
	userApplication.UserQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	tenantApplication.TenantQueryServiceInterface
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	awsSDKTypes.AWSSDKInterface
}

// ForgotTenantUserPassword forgot password
func (service *IAMCommandService) ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
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

	userEmail, err := tenantAuth.GetUserByEmail(ctx, email)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// check if email is verified
	if !userEmail.EmailVerified {
		log.Println("[error] email not verified.")
		return errors.New(apiError.FirebaseAuthEmailNotVerified)
	}

	resetLink, err := tenantAuth.PasswordResetLink(ctx, email)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// send to email
	textMessage := fmt.Sprintf("Hello,<br /><br />"+
		"Follow this link to reset your PACS AI password for your %s account:<br /><br />"+
		"%s<br /><br />"+
		"If you didn’t ask to reset your password, you can ignore this email.<br /><br />"+
		"Thanks,<br />"+
		"Your PACS AI team", email, resetLink)
	err = service.AWSSDKInterface.SESSendEmail(ctx, awsSDKTypes.SESSendEmailRequest{
		Subject:          "[PACS AI]: Reset password",
		ToAddresses:      []string{email},
		PlainTextMessage: textMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification code via aws ses", err)
		return errors.New(apiError.SESError)
	}

	return nil
}

// LoginTenantUser login tenant user by tenant
func (service *IAMCommandService) LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error) {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.FirebaseAuthError)
	}

	authToken, err := tenantAuth.VerifyIDToken(ctx, idToken)
	if err != nil {
		log.Println(err)
		return "", errors.New(apiError.UnauthorizedAccess)
	}

	// persist to token session cache
	sessionToken := generateID()

	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, authToken.UID)
	if err != nil {
		return "", err
	}

	err = service.SetTokenSession(ctx, types.SetTokenSession{
		SessionID:           sessionToken,
		TenantID:            tenantID,
		UserID:              user.ID,
		Role:                user.Role,
		ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
	})
	if err != nil {
		return "", err
	}

	// log to elasticsearch
	go func() {
		tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
		if err != nil {
			return
		}

		_, err = service.ElasticsearchCommandServiceInterface.CreateLoginLog(ctx, elasticsearchTypes.CreateLoginLog{
			SessionID:  sessionToken,
			TenantID:   tenantID,
			TenantName: tenant.Name,
			UserID:     user.ID,
			Email:      user.Email,
			Name:       user.Name,
			Role:       user.Role,
			Specialty:  user.Specialty,
		})
		if err != nil {
			log.Println(err)
			return
		}
	}()

	return sessionToken, nil
}

// SetTokenSession sets token session
func (service *IAMCommandService) SetTokenSession(ctx context.Context, data types.SetTokenSession) error {
	err := service.IAMCommandRepositoryInterface.SetTokenSession(repositoryTypes.SetTokenSession{
		SessionID:           data.SessionID,
		TenantID:            data.TenantID,
		UserID:              data.UserID,
		Role:                data.Role,
		ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
	})
	if err != nil {
		return err
	}

	return nil
}

// VerifyTenantUserEmail verifies tenant user email
func (service *IAMCommandService) VerifyTenantUserEmail(ctx context.Context, tenantID, email string) error {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
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

	_, err = tenantAuth.EmailVerificationLink(ctx, email)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// TODO: send to email

	return err
}

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
