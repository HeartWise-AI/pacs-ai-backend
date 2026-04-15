package application

import (
	"context"

	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantCommandServiceInterface holds the implementable methods for the tenant command service
type TenantCommandServiceInterface interface {
	// AddOnboardingQuestionnaireAnswers adds onboarding questionnaire answers
	AddOnboardingQuestionnaireAnswers(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error
	// RemoveOnboardingQuestionnaireAnswer removes an onboarding questionnaire answer
	RemoveOnboardingQuestionnaireAnswer(ctx context.Context, ID string) error
	// UpdateOnboardingConsentConfig updates the onboarding consent configuration
	UpdateOnboardingConsentConfig(ctx context.Context, data types.UpdateOnboardingConsentConfig) error
	// UpdateOnboardingRegistrationConfig updates the onboarding registration configuration
	UpdateOnboardingRegistrationConfig(ctx context.Context, data types.UpdateOnboardingRegistrationConfig) error
}
