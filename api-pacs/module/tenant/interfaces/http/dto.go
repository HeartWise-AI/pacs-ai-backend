package http

import (
	"api-pacs/module/tenant/domain/entity"

	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"AddOnboardingQuestionnaireAnswerRequest.QuestionnaireType":                                    "Questionnaire type is required.",
		"AddOnboardingQuestionnaireAnswerRequest.OnboardingQuestionnaireAnswers":                       "Onboarding questionnaire answers are required.",
		"AddOnboardingQuestionnaireAnswerRequest.OnboardingQuestionnaireAnswers.QuestionnaireID":       "Questionnaire ID is required.",
		"AddOnboardingQuestionnaireAnswerRequest.OnboardingQuestionnaireAnswers.QuestionnaireQuestion": "Questionnaire question is required.",
	}
)

type AddOnboardingQuestionnaireAnswerRequest struct {
	QuestionnaireType              entity.QuestionnaireType        `json:"questionnaireType" validate:"required"`
	OnboardingQuestionnaireAnswers []OnboardingQuestionnaireAnswer `json:"onboardingQuestionnaireAnswers" validate:"required"`
}

type GetOnboardingQuestionnaireAnswerResponse struct {
	ID                     string                   `json:"id"`
	TenantID               string                   `json:"tenantId"`
	UserID                 string                   `json:"userId"`
	QuestionnaireType      entity.QuestionnaireType `json:"questionnaireType"`
	QuestionnaireID        string                   `json:"questionnaireId"`
	QuestionnaireQuestion  string                   `json:"questionnaireQuestion"`
	QuestionnaireAnswerIDs []string                 `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string                 `json:"questionnaireAnswers"`
	CreatedAt              uint64                   `json:"createdAt"`
	UpdatedAt              uint64                   `json:"updatedAt"`
}

type GetTenantResponse struct {
	ID                       string                               `json:"id"`
	Name                     string                               `json:"name"`
	Address                  string                               `json:"address"`
	OnboardingQuestionnaires map[string][]OnboardingQuestionnaire `json:"onboardingQuestionnaires"`
	CreatedAt                uint                                 `json:"createdAt"`
	UpdatedAt                uint                                 `json:"updatedAt"`
}

type GetPublicTenantResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type OnboardingQuestionnaireAnswer struct {
	QuestionnaireID        string   `json:"questionnaireId" validate:"required"`
	QuestionnaireQuestion  string   `json:"questionnaireQuestion" validate:"required"`
	QuestionnaireAnswerIDs []string `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string `json:"questionnaireAnswers"`
}

type OnboardingQuestionnaire struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	QuestionEn      string         `json:"questionEn"`
	QuestionFr      string         `json:"questionFr"`
	AnswerOptionsEn []AnswerOption `json:"answerOptionsEn"`
	AnswerOptionsFr []AnswerOption `json:"answerOptionsFr"`
}

type AnswerOption struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}
