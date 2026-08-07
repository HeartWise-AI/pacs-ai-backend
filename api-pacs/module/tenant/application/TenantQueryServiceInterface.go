package application

import (
	"context"

	"api-pacs/module/tenant/domain/entity"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryServiceInterface holds the implementable methods for the tenant query service
type TenantQueryServiceInterface interface {
	// GetOnboardingQuestionnaireAnswers gets the onboarding questionnaire answers
	GetOnboardingQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingQuestionnaireAnswer) ([]entity.OnboardingQuestionnaireAnswer, error)
	// GetTenantByID gets tenant by id
	GetTenantByID(ctx context.Context, tenantID string) (types.GetTenantResult, error)
}
