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

type GetTenant struct {
	ID                       string
	Name                     string
	Address                  string
	OnboardingQuestionnaires map[string][]OnboardingQuestionnaire
	CreatedAt                uint
	UpdatedAt                uint
}

type OnboardingQuestionnaire struct {
	ID              string
	Type            string
	QuestionEn      string
	QuestionFr      string
	AnswerOptionsEn []AnswerOption
	AnswerOptionsFr []AnswerOption
}

type OnboardingQuestionnaireAnswer struct {
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type AnswerOption struct {
	ID     string
	Answer string
}
