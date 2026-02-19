package types

import "api-pacs/module/tenant/domain/entity"

type AddOnboardingQuestionnaireAnswer struct {
	ID                     string
	TenantID               string
	UserID                 string
	QuestionnaireType      entity.QuestionnaireType
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type GetOnboardingQuestionnaireAnswer struct {
	TenantID          string
	UserID            string
	QuestionnaireType *entity.QuestionnaireType
}

type GetTenant struct {
	ID        string
	Name      string
	Address   string
	CreatedAt uint
	UpdatedAt uint
}
