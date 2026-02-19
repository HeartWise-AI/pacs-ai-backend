package repository

import (
	"context"

	"api-pacs/module/tenant/domain/entity"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

type TenantQueryRepositoryInterface interface {
	SelectTenantByID(ctx context.Context, tenantID string) (repositoryTypes.GetTenant, error)
	SelectOnboardingQuestionnaireAnswers(ctx context.Context, data repositoryTypes.GetOnboardingQuestionnaireAnswer) ([]entity.OnboardingQuestionnaireAnswer, error)
}
