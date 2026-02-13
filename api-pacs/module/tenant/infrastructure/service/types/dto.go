package types

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
	AnswerOptions   []string
	AnswerOptionsFr []string
}
