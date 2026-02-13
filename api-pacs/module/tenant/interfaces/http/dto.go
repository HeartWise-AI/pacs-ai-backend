package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{}
)

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

type OnboardingQuestionnaire struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	QuestionEn      string   `json:"questionEn"`
	QuestionFr      string   `json:"questionFr"`
	AnswerOptions   []string `json:"answerOptions"`
	AnswerOptionsFr []string `json:"answerOptionsFr"`
}
