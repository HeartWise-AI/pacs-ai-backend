package service

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	docusignTypes "api-pacs/infrastructures/providers/api/docusign/types"
	elasticsearchAppliction "api-pacs/module/elasticsearch/application"
	elasticsearchTypes "api-pacs/module/elasticsearch/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserQueryService handles the User query service logic
type UserQueryService struct {
	repository.UserQueryRepositoryInterface
	repository.UserCommandRepositoryInterface
	tenantApplication.TenantQueryServiceInterface
	elasticsearchAppliction.ElasticsearchCommandServiceInterface
	docusignTypes.DocusignAPIInterface
}

// GetDoctorSpecialties get doctor specialties
func (service *UserQueryService) GetDoctorSpecialties(ctx context.Context) ([]map[string]interface{}, error) {
	rootPath, _ := os.Getwd()
	jsonData, err := os.ReadFile(filepath.Join(rootPath, "internal/specialties/specialties.json"))
	if err != nil {
		return []map[string]interface{}{}, err
	}

	var specialties []map[string]interface{}
	err = json.Unmarshal(jsonData, &specialties)
	if err != nil {
		return []map[string]interface{}{}, err
	}

	return specialties, nil
}

// GetTenantUserByID get tenant user by id
func (service *UserQueryService) GetTenantUserByID(ctx context.Context, tenantID, ID string) (types.GetTenantUser, error) {
	user, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, tenantID, ID)
	if err != nil {
		return types.GetTenantUser{}, err
	}

	// get tenant
	tenant, err := service.TenantQueryServiceInterface.GetTenantByID(ctx, tenantID)
	if err != nil {
		return types.GetTenantUser{}, err
	}

	// if consent is enabled and user haven't signed, update consent status
	if tenant.OnboardingEnableConsent && !user.IsConsentSigned {
		err := service.updateTenantUserConsentStatus(ctx, tenant, ID)
		if err != nil {
			log.Println("Failed to update user consent status: ", err) // silent error
		}
	}

	return types.GetTenantUser{
		ID:                user.ID,
		TenantID:          user.TenantID,
		Role:              user.Role,
		Name:              user.Name,
		Email:             user.Email,
		LicenseNo:         user.LicenseNo,
		Specialty:         user.Specialty,
		IsEmailVerified:   user.IsEmailVerified,
		IsAccountDisabled: user.IsAccountDisabled,
		IsConsentSigned:   user.IsConsentSigned,
		CreatedAt:         uint(user.CreatedAt),
		UpdatedAt:         uint(user.UpdatedAt),
	}, nil
}

// GetTenantUsers get tenant users
func (service *UserQueryService) GetTenantUsers(ctx context.Context, tenantID string) ([]types.GetTenantUser, error) {
	res, err := service.UserQueryRepositoryInterface.SelectTenantUsers(ctx, tenantID)
	if err != nil {
		return []types.GetTenantUser{}, err
	}

	var users []types.GetTenantUser

	for _, user := range res {
		users = append(users, types.GetTenantUser{
			ID:                user.ID,
			TenantID:          user.TenantID,
			Role:              user.Role,
			Name:              user.Name,
			Email:             user.Email,
			LicenseNo:         user.LicenseNo,
			Specialty:         user.Specialty,
			IsEmailVerified:   user.IsEmailVerified,
			IsAccountDisabled: user.IsAccountDisabled,
			CreatedAt:         user.CreatedAt,
			UpdatedAt:         user.UpdatedAt,
		})
	}

	return users, nil
}

// GetUserMetadata get user metadata
func (service *UserQueryService) GetUserMetadata(ctx context.Context, userID string) (entity.UserMetadata, error) {
	userMetadata, err := service.UserQueryRepositoryInterface.SelectUserMetadataByID(ctx, userID)
	if err != nil {
		return entity.UserMetadata{}, err
	}

	return userMetadata, nil
}

// updateTenantUserConsentStatus update tenant user consent status by user id
func (service *UserQueryService) updateTenantUserConsentStatus(ctx context.Context, tenant tenantTypes.GetTenantResult, userID string) error {
	/// get user
	user, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, tenant.ID, userID)
	if err != nil {
		log.Println(err)
		return err
	}

	/// check from docusign
	accessToken, err := service.DocusignAPIInterface.GetAccessToken()
	if err != nil {
		return err
	}

	// get docusign envelopes
	// FIXME: this defaults to 5 years ago with from_date (apparently it is required)
	fromDate := time.Now().AddDate(-5, 0, 0).Format("2006-01-02")
	envelopes, err := service.DocusignAPIInterface.GetEnvelopes(accessToken, docusignTypes.GetEnvelopeRequest{
		FromDate:   fromDate,
		SearchText: user.Email,
		Include:    "recipients", // https://developers.docusign.com/docs/esign-rest-api/reference/envelopes/envelopes/liststatuschanges/
	})
	if err != nil {
		return err
	}

	// check email and status from envelopes
	var userFound bool

	for _, envelope := range envelopes {
		if envelope.Recipients == nil {
			continue
		}

		for _, recipient := range envelope.Recipients.Signers {
			if strings.EqualFold(recipient.Email, user.Email) && recipient.Status == docusignTypes.EnvelopeStatusCompleted {
				userFound = true
				break
			}
		}

		if userFound {
			break
		}
	}

	/// if found, save to cache
	if userFound {
		err := service.UserCommandRepositoryInterface.UpdateTenantUserConsent(ctx, repositoryTypes.UpdateTenantUserConsent{
			ID:              user.ID,
			IsConsentSigned: true,
		})
		if err != nil {
			log.Println("Failed to update user consent: ", err) // silent error
			return err
		}

		// log to elasticsearch
		go func() {
			_, err := service.ElasticsearchCommandServiceInterface.CreateSignedConsentLog(ctx, elasticsearchTypes.CreateSignedConsentLog{
				TenantID:   tenant.ID,
				TenantName: tenant.Name,
				UserID:     user.ID,
				Email:      user.Email,
			})
			if err != nil {
				log.Println(err)
				return
			}
		}()
	}

	return nil
}
