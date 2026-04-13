package repository

import (
	"context"

	"api-pacs/module/tenant/infrastructure/repository/types"
)

type TenantCommandRepositoryInterface interface {
	// DeleteOnboardingQuestionnaireAnswer deletes an onboarding questionnaire answer
	DeleteOnboardingQuestionnaireAnswer(ctx context.Context, ID string) error
	// InsertOnboardingQuestionnaireAnswer inserts a onboarding questionnaire answer
	InsertOnboardingQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error
	// UpdateOnboardingEnableConsent updates the onboarding enable consent
	UpdateOnboardingEnableConsent(ctx context.Context, data types.UpdateOnboardingEnableConsent) error
	// UpdateOnboardingEnableRegistration updates the onboarding enable registration
	UpdateOnboardingEnableRegistration(ctx context.Context, data types.UpdateOnboardingEnableRegistration) error
}
