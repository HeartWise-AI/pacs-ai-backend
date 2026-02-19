package entity

type QuestionnaireType string

const (
	PreSurveyQuestionnaireType  QuestionnaireType = "PRE_SURVEY"
	PostSurveyQuestionnaireType QuestionnaireType = "POST_SURVEY"
)

// OnboardingQuestionnaireAnswer holds the onboarding questionnaire answer entity fields
type OnboardingQuestionnaireAnswer struct {
	ID                     string            `firestore:"id,omitempty"`
	TenantID               string            `firestore:"tenant_id"`
	UserID                 string            `firestore:"user_id"`
	QuestionnaireType      QuestionnaireType `firestore:"questionnaire_type"` // enum
	QuestionnaireID        string            `firestore:"questionnaire_id"`
	QuestionnaireQuestion  string            `firestore:"questionnaire_question"`
	QuestionnaireAnswerIDs []string          `firestore:"questionnaire_answer_ids"`
	QuestionnaireAnswers   []string          `firestore:"questionnaire_answers"`
	CreatedAt              int               `firestore:"created_at"`
	UpdatedAt              int               `firestore:"updated_at"`
}

// GetModelName returns the model name of onboarding questionnaire answer entity that can be used for naming schemas
func (entity *OnboardingQuestionnaireAnswer) GetModelName() string {
	return "onboarding_questionnaire_answers"
}
