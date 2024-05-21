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
	"api-pacs/module/elasticsearch/domain/repository"
	userApplication "api-pacs/module/user/application"
)

// ElasticsearchCommandService handles the Elasticsearch command service logic
type ElasticsearchCommandService struct {
	repository.ElasticsearchCommandRepositoryInterface
	userApplication.UserQueryServiceInterface
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
	awsSDKTypes.AWSSDKInterface
}

// ForgotTenantUserPassword forgot password
func (service *ElasticsearchCommandService) ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error {
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

// generateID generates unique id
func generateID() string {
	return ksuid.New().String()
}
