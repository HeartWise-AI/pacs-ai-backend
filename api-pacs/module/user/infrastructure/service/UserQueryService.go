package service

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	docusignTypes "api-pacs/infrastructures/providers/api/docusign/types"
	apiError "api-pacs/internal/errors"
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
	PolicyCatalog *PolicyCatalog
}

// GetRegistrationPolicies returns deployment-authoritative policy metadata for
// an unauthenticated tenant-aware registration screen.
func (service *UserQueryService) GetRegistrationPolicies(_ context.Context, tenantID string) ([]types.PolicyDefinition, error) {
	return service.PolicyCatalog.CurrentPolicies(tenantID)
}

// GetPolicyStatus reports acceptance only for the current required versions.
func (service *UserQueryService) GetPolicyStatus(ctx context.Context, tenantID, userID string) (types.PolicyStatus, error) {
	policies, err := service.PolicyCatalog.CurrentPolicies(tenantID)
	if err != nil {
		return types.PolicyStatus{}, err
	}
	references := make([]entity.PolicyReference, 0, len(policies))
	for _, policy := range policies {
		if policy.Required {
			references = append(references, entity.PolicyReference{PolicyKey: policy.PolicyKey, Version: policy.Version})
		}
	}
	accepted, err := service.UserQueryRepositoryInterface.SelectUserPolicyAcceptances(ctx, tenantID, userID, references)
	if err != nil {
		return types.PolicyStatus{}, err
	}
	acceptedByReference := make(map[entity.PolicyReference]int64, len(accepted))
	for _, acceptance := range accepted {
		acceptedByReference[acceptance.Reference()] = acceptance.AcceptedAt
	}

	status := types.PolicyStatus{Policies: make([]types.PolicyStatusItem, 0, len(policies))}
	status.EnforcementActive, err = service.PolicyCatalog.EnforcementActive(tenantID, time.Now())
	if err != nil {
		return types.PolicyStatus{}, err
	}
	for _, policy := range policies {
		acceptedAt, isAccepted := acceptedByReference[entity.PolicyReference{PolicyKey: policy.PolicyKey, Version: policy.Version}]
		item := types.PolicyStatusItem{PolicyDefinition: policy, Accepted: isAccepted}
		if isAccepted {
			item.AcceptedAt = &acceptedAt
		}
		if policy.Required && !isAccepted {
			status.AcceptanceRequired = true
		}
		status.Policies = append(status.Policies, item)
	}
	return status, nil
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
		isConsentSigned, err := service.updateTenantUserConsentStatus(ctx, tenant, ID)
		if err != nil {
			log.Println("Failed to update user consent status: ", err) // silent error
		}

		// override user result
		user.IsConsentSigned = isConsentSigned
	}

	return types.GetTenantUser{
		ID:                user.ID,
		TenantID:          user.TenantID,
		Role:              user.Role,
		AccessState:       user.AccessState,
		Name:              user.Name,
		Email:             user.Email,
		LicenseNo:         user.LicenseNo,
		Specialty:         user.Specialty,
		IsEmailVerified:   user.IsEmailVerified,
		IsAccountDisabled: user.IsAccountDisabled,
		IsConsentSigned:   user.IsConsentSigned,
		IsAdminCreated:    user.IsAdminCreated,
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
			AccessState:       user.AccessState,
			Name:              user.Name,
			Email:             user.Email,
			LicenseNo:         user.LicenseNo,
			Specialty:         user.Specialty,
			IsEmailVerified:   user.IsEmailVerified,
			IsAccountDisabled: user.IsAccountDisabled,
			IsConsentSigned:   user.IsConsentSigned,
			IsAdminCreated:    user.IsAdminCreated,
			CreatedAt:         user.CreatedAt,
			UpdatedAt:         user.UpdatedAt,
		})
	}

	return users, nil
}

// GetTenantUserEmailInvites get tenant user email invites
func (service *UserQueryService) GetTenantUserEmailInvites(ctx context.Context, tenantID string) ([]entity.UserEmailInvite, error) {
	emailInvites, err := service.UserQueryRepositoryInterface.SelectTenantUserEmailInvites(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return []entity.UserEmailInvite{}, err
	}

	return emailInvites, nil
}

// GetUserMetadata get user metadata
func (service *UserQueryService) GetUserMetadata(ctx context.Context, userID string) (entity.UserMetadata, error) {
	userMetadata, err := service.UserQueryRepositoryInterface.SelectUserMetadataByID(ctx, userID)
	if err != nil {
		return entity.UserMetadata{}, err
	}

	return userMetadata, nil
}

func (service *UserQueryService) updateTenantUserConsentStatus(ctx context.Context, tenant tenantTypes.GetTenantResult, userID string) (bool, error) {
	/// get user
	user, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, tenant.ID, userID)
	if err != nil {
		log.Println(err)
		return false, err
	}

	/// check from docusign
	accessToken, err := service.DocusignAPIInterface.GetAccessToken()
	if err != nil {
		return false, err
	}

	// get the consent powerform id
	var powerFormID string

	if len(tenant.OnboardingConsentLink) > 0 {
		parsedConsentURL, err := url.Parse(tenant.OnboardingConsentLink)
		if err != nil {
			log.Println(err)
			return false, err
		}

		powerFormID = path.Base(parsedConsentURL.Path)
	}

	// get docusign envelopes
	// FIXME: this defaults to 10 years ago with from_date (apparently it is required but negligible in terms of request time)
	fromDate := time.Now().AddDate(-10, 0, 0).Format("2006-01-02")
	envelopes, err := service.DocusignAPIInterface.GetEnvelopes(accessToken, docusignTypes.GetEnvelopeRequest{
		FromDate:     fromDate,
		SearchText:   user.Email,
		Include:      "recipients", // https://developers.docusign.com/docs/esign-rest-api/reference/envelopes/envelopes/liststatuschanges/
		PowerFormIDs: powerFormID,
	})
	if err != nil {
		return false, err
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
			return false, err
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

	return userFound, nil
}
