package entity

// OnboardingModelQuestionnaireAnswer holds the onboarding model questionnaire answer entity fields
type OnboardingModelQuestionnaireAnswer struct {
	ID                     string   `firestore:"id,omitempty"`
	TenantID               string   `firestore:"tenant_id"`
	UserID                 string   `firestore:"user_id"`
	ModelID                string   `firestore:"model_id"`
	QuestionnaireID        string   `firestore:"questionnaire_id"`
	QuestionnaireQuestion  string   `firestore:"questionnaire_question"`
	QuestionnaireAnswerIDs []string `firestore:"questionnaire_answer_ids"`
	QuestionnaireAnswers   []string `firestore:"questionnaire_answers"`
	CreatedAt              int      `firestore:"created_at"`
	UpdatedAt              int      `firestore:"updated_at"`
}

// GetModelName returns the model name of onboarding model questionnaire answer entity that can be used for naming schemas
func (entity *OnboardingModelQuestionnaireAnswer) GetModelName() string {
	return "onboarding_model_questionnaire_answers"
}
