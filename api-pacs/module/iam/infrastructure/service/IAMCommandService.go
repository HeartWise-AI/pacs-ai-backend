package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/segmentio/ksuid"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/infrastructures/providers/sdk/mailgun"
	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/iam/domain/repository"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	"api-pacs/module/iam/infrastructure/service/types"
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
	MailgunSDK       *mailgun.MailgunSDK
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
	err = service.MailgunSDK.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Reset password",
		Recipient:     email,
		PlainTextBody: textMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification code via mailgun", err)
		return errors.New(apiError.MailgunError)
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
	if !user.IsEmailVerified && !user.IsAdminCreated {
		return "", errors.New(apiError.FirebaseAuthEmailNotVerified)
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
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	cooldownKey := fmt.Sprintf("email_verification:%s:%s", tenantID, normalizedEmail)
	cooldownActive, err := service.IAMCommandRepositoryInterface.IsEmailVerificationCooldownActive(cooldownKey)
	if err != nil {
		return err
	}
	if cooldownActive {
		return errors.New(apiError.MaximumLimitReached)
	}

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

	verifyLink, err := tenantAuth.EmailVerificationLink(ctx, normalizedEmail)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	if err := service.IAMCommandRepositoryInterface.SetEmailVerificationCooldown(cooldownKey); err != nil {
		return err
	}

	textMessage := fmt.Sprintf("Hello,<br /><br />"+
		"Follow this link to verify your PACS AI email address for your %s account:<br /><br />"+
		"<a href=\"%s\">%s</a><br /><br />"+
		"If you did not create a PACS AI account, you can ignore this email.<br /><br />"+
		"Thanks,<br />"+
		"Your PACS AI team", normalizedEmail, verifyLink, verifyLink)
	err = service.MailgunSDK.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Verify your email",
		Recipient:     normalizedEmail,
		PlainTextBody: textMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification email via mailgun", err)
		return errors.New(apiError.MailgunError)
	}

	return nil
}

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
