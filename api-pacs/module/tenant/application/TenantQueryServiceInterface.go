package application

import (
	"context"

	"api-pacs/module/tenant/domain/entity"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryServiceInterface holds the implementable methods for the tenant query service
type TenantQueryServiceInterface interface {
	GetOnboardingQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingQuestionnaireAnswer) ([]entity.OnboardingQuestionnaireAnswer, error)
	GetTenantByID(ctx context.Context, tenantID string) (types.GetTenant, error)
}
