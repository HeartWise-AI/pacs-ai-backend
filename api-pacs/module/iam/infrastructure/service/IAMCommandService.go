package service

import (
	"context"

	"github.com/segmentio/ksuid"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/iam/domain/repository"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	userApplication "api-pacs/module/user/application"
)

// IAMCommandService handles the IAM command service logic
type IAMCommandService struct {
	repository.IAMCommandRepositoryInterface
	userApplication.UserQueryServiceInterface
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
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

	_, err = tenantAuth.PasswordResetLink(ctx, email)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirebaseAuthError)
	}

	// TODO: send to email

	return nil
}

// LoginTenantUser login tenant user by tenant
func (service *IAMCommandService) LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error) {
	uid := "7Fu9yYoxZPSQjMvYdS2be54Crwr1"

	// firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	// if err != nil {
	// 	log.Println(err)
	// 	return "", errors.New(apiError.FirebaseAuthError)
	// }

	// // tenant auth
	// tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	// if err != nil {
	// 	log.Println(err)
	// 	return "", errors.New(apiError.FirebaseAuthError)
	// }

	// authToken, err := tenantAuth.VerifyIDToken(ctx, idToken)
	// if err != nil {
	// 	log.Println(err)
	// 	return "", errors.New(apiError.UnauthorizedAccess)
	// }

	// persist to token session cache
	sessionToken := generateID()

	user, err := service.UserQueryServiceInterface.GetTenantUserByID(ctx, tenantID, uid)
	if err != nil {
		return "", err
	}

	err = service.IAMCommandRepositoryInterface.SetTokenSession(repositoryTypes.SetTokenSession{
		SessionID:           sessionToken,
		TenantID:            tenantID,
		UserID:              user.ID,
		Role:                user.Role,
		ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
	})
	if err != nil {
		return "", err
	}

	return sessionToken, nil
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
