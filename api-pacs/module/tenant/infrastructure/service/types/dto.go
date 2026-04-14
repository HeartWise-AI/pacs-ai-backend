package types

import "api-pacs/module/tenant/domain/entity"

type AddOnboardingQuestionnaireAnswer struct {
	ID                             string
	TenantID                       string
	UserID                         string
	QuestionnaireType              entity.QuestionnaireType
	OnboardingQuestionnaireAnswers []OnboardingQuestionnaireAnswer
}

type GetOnboardingQuestionnaireAnswer struct {
	TenantID          string
	UserID            string
	QuestionnaireType *entity.QuestionnaireType
}

type GetTenantResult struct {
	ID                           string
	Name                         string
	Address                      string
	OnboardingQuestionnaires     *string
	OnboardingEnableConsent      bool
	OnboardingEnableRegistration bool
	OnboardingConsentLink        string
	CreatedAt                    uint
	UpdatedAt                    uint
}

type OnboardingQuestionnaireAnswer struct {
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type UpdateOnboardingConsentConfig struct {
	TenantID                string
	OnboardingEnableConsent bool
}

type UpdateOnboardingRegistrationConfig struct {
	TenantID                     string
	OnboardingEnableRegistration bool
}

type AnswerOption struct {
	ID     string
	Answer string
}
