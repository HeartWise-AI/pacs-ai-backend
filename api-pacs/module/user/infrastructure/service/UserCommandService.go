package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/segmentio/ksuid"

	mailgunTypes "api-pacs/infrastructures/providers/sdk/mailgun/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/entity"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	inferenceApplication "api-pacs/module/inference/application"
	inferenceTypes "api-pacs/module/inference/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
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
	tenantApplication.TenantCommandServiceInterface
	tenantApplication.TenantQueryServiceInterface
	inferenceApplication.InferenceCommandServiceInterface
	inferenceApplication.InferenceQueryServiceInterface
	elasticsearchApplication.ElasticsearchCommandServiceInterface
	mailgunTypes.MailgunSDKInterface
}

const (
	userSingleTenantLoginTemplate  string = "%s://%s/login"
	userMultiTenantLoginTemplate   string = "%s://%s.%s/login"
	userSingleTenantInviteTemplate string = "%s://%s/user/invite/?tenant=%s&email=%s&code=%s"
	userMultiTenantsInviteTemplate string = "%s://%s/user/invite/?tenant=%s&email=%s&code=%s"
)

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

// ResetTutorial resets the tutorial for a user
func (service *UserCommandService) ResetTutorial(ctx context.Context, data types.ResetTutorial) error {
	// get onboarding questionnaire answers
	onboardingQuestionnaireAnswers, err := service.TenantQueryServiceInterface.GetOnboardingQuestionnaireAnswers(ctx, tenantTypes.GetOnboardingQuestionnaireAnswer{
		TenantID: data.TenantID,
		UserID:   data.UserID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// remove onboarding questionnaire answers
	for _, onboardingQuestionnaireAnswer := range onboardingQuestionnaireAnswers {
		err = service.TenantCommandServiceInterface.RemoveOnboardingQuestionnaireAnswer(ctx, onboardingQuestionnaireAnswer.ID)
		if err != nil {
			return err
		}
	}

	// get onboarding model questionnaire answers
	onboardingModelQuestionnaireAnswers, err := service.InferenceQueryServiceInterface.GetOnboardingModelQuestionnaireAnswers(ctx, inferenceTypes.GetOnboardingModelQuestionnaireAnswer{
		TenantID: data.TenantID,
		UserID:   data.UserID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return err
	}

	// remove onboarding model questionnaire answers
	for _, onboardingModelQuestionnaireAnswer := range onboardingModelQuestionnaireAnswers {
		err = service.InferenceCommandServiceInterface.RemoveOnboardingModelQuestionnaireAnswer(ctx, onboardingModelQuestionnaireAnswer.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

// ResendTenantUserEmailInvite resends a tenant user email invite to the email
func (service *UserCommandService) ResendTenantUserEmailInvite(ctx context.Context, data types.ResendTenantUserEmailInvite) error {
	// get tenant
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
	if err != nil {
		return err
	}

	// check if registration is enabled
	if !tenant.OnboardingEnableRegistration {
		return errors.New(apiError.ForbiddenAccess)
	}

	// get tenant user email invite by id
	userInviteByID, err := service.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByID(ctx, data.TenantID, data.ID)
	if err != nil {
		return err
	}

	// generate code
	code := generateID()

	// update tenant user invite code and expiration
	err = service.UserCommandRepositoryInterface.UpdateTenantUserEmailInvite(ctx, repositoryTypes.UpdateTenantUserEmailInvite{
		ID:        userInviteByID.ID,
		Code:      code,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days expiration
	})
	if err != nil {
		return err
	}

	// generate url
	var url string
	if strings.EqualFold(os.Getenv("API_ENABLE_MULTI_TENANT"), "true") {
		url = fmt.Sprintf(userMultiTenantsInviteTemplate, os.Getenv("APP_SCHEME"), os.Getenv("APP_HOST"), data.TenantID, userInviteByID.Email, code)
	} else {
		url = fmt.Sprintf(userSingleTenantInviteTemplate, os.Getenv("APP_SCHEME"), os.Getenv("APP_HOST"), data.TenantID, userInviteByID.Email, code)
	}

	// send to email
	redirectURL := url

	emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
		"You have been invited to join PACS AI. Please click the link below to accept the invitation: <br /><br />"+
		"<a href=\"%s\">%s</a> <br /><br />"+
		"Thanks, <br /><br />"+
		"Your PACS AI Team", userInviteByID.Email, redirectURL, redirectURL)

	err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Invitation to join workspace",
		Recipient:     userInviteByID.Email,
		PlainTextBody: emailMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification code via mailgun", err)
		return errors.New(apiError.MailgunError)
	}

	return nil
}

// SendTenantUserEmailInvite sends a tenant user email invite to the email
func (service *UserCommandService) SendTenantUserEmailInvite(ctx context.Context, data types.SendTenantUserEmailInvite) error {
	// get tenant
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, data.TenantID)
	if err != nil {
		return err
	}

	// check if registration is enabled
	if !tenant.OnboardingEnableRegistration {
		return errors.New(apiError.ForbiddenAccess)
	}

	// check if email invite already exists
	_, err = service.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByEmail(ctx, data.TenantID, data.Email)
	if err == nil {
		return errors.New(apiError.DuplicateRecord)
	} else if err.Error() != apiError.MissingRecord {
		return err
	}

	// check if email already exists in users
	err = service.UserQueryRepositoryInterface.SelectTenantUserByEmail(ctx, data.TenantID, data.Email)
	if err == nil {
		return errors.New(apiError.DuplicateRecord)
	} else if err.Error() != apiError.MissingRecord {
		return err
	}

	// generate code
	code := generateID()

	// insert tenant user invite
	err = service.UserCommandRepositoryInterface.InsertTenantUserEmailInvite(ctx, repositoryTypes.CreateTenantUserEmailInvite{
		ID:        generateID(),
		TenantID:  data.TenantID,
		Code:      code,
		Email:     data.Email,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days expiration
	})
	if err != nil {
		return err
	}

	// TODO: log to elasticsearch

	// generate url
	var url string
	if strings.EqualFold(os.Getenv("API_ENABLE_MULTI_TENANT"), "true") {
		url = fmt.Sprintf(userMultiTenantsInviteTemplate, os.Getenv("APP_SCHEME"), os.Getenv("APP_HOST"), data.TenantID, data.Email, code)
	} else {
		url = fmt.Sprintf(userSingleTenantInviteTemplate, os.Getenv("APP_SCHEME"), os.Getenv("APP_HOST"), data.TenantID, data.Email, code)
	}

	// send to email
	redirectURL := url

	emailMessage := fmt.Sprintf("Hi %s, <br /><br />"+
		"You have been invited to join PACS AI. Please click the link below to accept the invitation: <br /><br />"+
		"<a href=\"%s\">%s</a> <br /><br />"+
		"Thanks, <br /><br />"+
		"Your PACS AI Team", data.Email, redirectURL, redirectURL)

	err = service.MailgunSDKInterface.SendEmail(ctx, mailgunTypes.MailgunSendEmailRequest{
		Subject:       "[PACS AI]: Invitation to join workspace",
		Recipient:     data.Email,
		PlainTextBody: emailMessage,
	})
	if err != nil {
		log.Println("[error] cannot send verification code via mailgun", err)
		return errors.New(apiError.MailgunError)
	}

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
	metadataBytes, err := json.Marshal(data.Metadata)
	if err != nil {
		return err
	}

	err = service.UserCommandRepositoryInterface.UpsertUserMetadata(ctx, repositoryTypes.UpsertUserMetadata{
		UserID:   data.UserID,
		Metadata: string(metadataBytes),
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
