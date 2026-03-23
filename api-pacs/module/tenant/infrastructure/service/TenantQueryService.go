package service

import (
	"context"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/domain/entity"
	"api-pacs/module/tenant/domain/repository"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryService handles the Tenant query service logic
type TenantQueryService struct {
	repository.TenantQueryRepositoryInterface
}

// GetOnboardingQuestionnaireAnswers gets the onboarding questionnaire answers
func (service *TenantQueryService) GetOnboardingQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingQuestionnaireAnswer) ([]entity.OnboardingQuestionnaireAnswer, error) {
	res, err := service.TenantQueryRepositoryInterface.SelectOnboardingQuestionnaireAnswers(ctx, repositoryTypes.GetOnboardingQuestionnaireAnswer{
		TenantID:          data.TenantID,
		UserID:            data.UserID,
		QuestionnaireType: data.QuestionnaireType,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return nil, err
	}

	return res, nil
}

// GetTenantByID get tenant by id
func (service *TenantQueryService) GetTenantByID(ctx context.Context, tenantID string) (types.GetTenant, error) {
	tenant, err := service.TenantQueryRepositoryInterface.SelectTenantByID(ctx, tenantID)
	if err != nil {
		return types.GetTenant{}, err
	}

	return types.GetTenant{
		ID:                       tenant.ID,
		Name:                     tenant.Name,
		Address:                  tenant.Address,
		OnboardingQuestionnaires: tenant.OnboardingQuestionnaires,
		CreatedAt:                uint(tenant.CreatedAt),
		UpdatedAt:                uint(tenant.UpdatedAt),
	}, nil
}
