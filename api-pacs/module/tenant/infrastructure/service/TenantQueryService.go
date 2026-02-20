package service

import (
	"context"

	apiError "api-pacs/internal/errors"
	tenantUtil "api-pacs/internal/tenant"
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

	onboardingQuestionnairesRes := map[string][]types.OnboardingQuestionnaire{}
	for questionnaireType, questionnaires := range tenantUtil.OnboardingQuestionnaires {
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
