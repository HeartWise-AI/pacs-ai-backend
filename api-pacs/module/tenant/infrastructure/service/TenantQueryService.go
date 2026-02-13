package service

import (
	"context"

	"api-pacs/internal/inference"
	"api-pacs/module/tenant/domain/repository"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryService handles the Tenant query service logic
type TenantQueryService struct {
	repository.TenantQueryRepositoryInterface
}

// GetTenantByID get tenant by id
func (service *TenantQueryService) GetTenantByID(ctx context.Context, tenantID string) (types.GetTenant, error) {
	tenant, err := service.TenantQueryRepositoryInterface.SelectTenantByID(ctx, tenantID)
	if err != nil {
		return types.GetTenant{}, err
	}

	onboardingQuestionnairesRes := map[string][]types.OnboardingQuestionnaire{}
	for questionnaireType, questionnaires := range inference.OnboardingQuestionnaires {
		var questionnairesRes []types.OnboardingQuestionnaire

		for _, questionnaire := range questionnaires {
			questionnairesRes = append(questionnairesRes, types.OnboardingQuestionnaire{
				ID:              questionnaire.ID,
				Type:            questionnaire.Type,
				QuestionEn:      questionnaire.QuestionEn,
				QuestionFr:      questionnaire.QuestionFr,
				AnswerOptionsEn: questionnaire.AnswerOptionsEn,
				AnswerOptionsFr: questionnaire.AnswerOptionsFr,
			})
		}

		onboardingQuestionnairesRes[questionnaireType] = questionnairesRes
	}

	return types.GetTenant{
		ID:                       tenant.ID,
		Name:                     tenant.Name,
		Address:                  tenant.Address,
		OnboardingQuestionnaires: onboardingQuestionnairesRes,
		CreatedAt:                uint(tenant.CreatedAt),
		UpdatedAt:                uint(tenant.UpdatedAt),
	}, nil
}
