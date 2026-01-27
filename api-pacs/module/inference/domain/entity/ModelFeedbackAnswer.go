package entity

// ModelFeedbackAnswer holds the model feedback answer entity fields
type ModelFeedbackAnswer struct {
	ID                     string   `firestore:"id,omitempty"`
	ModelFeedbackID        string   `firestore:"model_feedback_id"`
	QuestionnaireID        string   `firestore:"questionnaire_id"`
	QuestionnaireQuestions []string `firestore:"questionnaire_questions"`
	QuestionnaireAnswers   []string `firestore:"questionnaire_answers"`
	CreatedAt              int      `firestore:"created_at"`
	UpdatedAt              int      `firestore:"updated_at"`
}

// GetModelName returns the model name of model feedback answer entity that can be used for naming schemas
func (entity *ModelFeedbackAnswer) GetModelName() string {
	return "model_feedback_answers"
}
