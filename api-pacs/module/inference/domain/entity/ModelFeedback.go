package entity

type FeedbackType string

const (
	ApproveFeedbackType FeedbackType = "APPROVE"
	RejectFeedbackType  FeedbackType = "REJECT"
)

// ModelFeedback holds the model feedback entity fields
type ModelFeedback struct {
	ID               string       `firestore:"id,omitempty"`
	TenantID         string       `firestore:"tenant_id"`
	UserID           string       `firestore:"user_id"`
	InferenceModelID string       `firestore:"inference_model_id"`
	ModelID          string       `firestore:"model_id"`
	FeedbackType     FeedbackType `firestore:"feedback_type"`
	CreatedAt        int          `firestore:"created_at"`
	UpdatedAt        int          `firestore:"updated_at"`
	// RunID, ExecutionID and StudyServiceJobID reference the report the user
	// was viewing when they last set the vote. Absent on legacy documents.
	RunID             string `firestore:"run_id,omitempty"`
	ExecutionID       string `firestore:"execution_id,omitempty"`
	StudyServiceJobID string `firestore:"study_service_job_id,omitempty"`
}

// GetModelName returns the model name of model feedback entity that can be used for naming schemas
func (entity *ModelFeedback) GetModelName() string {
	return "model_feedbacks"
}
