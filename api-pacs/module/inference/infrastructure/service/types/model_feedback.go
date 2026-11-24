package types

import (
	"time"

	"api-pacs/module/inference/domain/entity"
)

// ListModelFeedback is an authorized admin request for the feedback of a
// tenant. TenantID and Role always come from the authenticated session.
type ListModelFeedback struct {
	TenantID string
	Role     string
	// CanView is true when the session holds feedback.view (custom roles).
	CanView bool
	// Model matches either the versioned model id or the inference model id.
	Model string
	// FeedbackType is empty, APPROVE or REJECT.
	FeedbackType string
	// From and To bound updated_at (inclusive).
	From *time.Time
	To   *time.Time
}

// ModelFeedbackList is the admin view of a tenant's model feedback.
type ModelFeedbackList struct {
	Summary []ModelFeedbackSummary  `json:"summary"`
	Items   []ModelFeedbackListItem `json:"items"`
	// Truncated reports that Items was capped; Summary always covers every match.
	Truncated bool `json:"truncated"`
}

// ModelFeedbackSummary counts the votes of one model version.
type ModelFeedbackSummary struct {
	ModelID          string `json:"modelId"`
	InferenceModelID string `json:"inferenceModelId"`
	Version          string `json:"version"`
	ApproveCount     int    `json:"approveCount"`
	RejectCount      int    `json:"rejectCount"`
	// LastUpdatedAt is the newest updated_at, in unix seconds.
	LastUpdatedAt int `json:"lastUpdatedAt"`
}

// ModelFeedbackListItem is one vote with its answers.
type ModelFeedbackListItem struct {
	ID                string                    `json:"id"`
	UserID            string                    `json:"userId"`
	UserName          *string                   `json:"userName"`
	UserEmail         *string                   `json:"userEmail"`
	InferenceModelID  string                    `json:"inferenceModelId"`
	ModelID           string                    `json:"modelId"`
	ModelVersion      string                    `json:"modelVersion"`
	FeedbackType      entity.FeedbackType       `json:"feedbackType"`
	CreatedAt         int                       `json:"createdAt"`
	UpdatedAt         int                       `json:"updatedAt"`
	RunID             string                    `json:"runId"`
	ExecutionID       string                    `json:"executionId"`
	StudyServiceJobID string                    `json:"studyServiceJobId"`
	Answers           []ModelFeedbackListAnswer `json:"answers"`
}

// ModelFeedbackListAnswer is one questionnaire answer of a vote.
type ModelFeedbackListAnswer struct {
	QuestionnaireID        string   `json:"questionnaireId"`
	QuestionnaireQuestion  string   `json:"questionnaireQuestion"`
	QuestionnaireAnswerIDs []string `json:"questionnaireAnswerIds"`
	QuestionnaireAnswers   []string `json:"questionnaireAnswers"`
}

// ListFeedbackReportCounts is an authorized admin request for the number of
// reports returned per model in [From, To). TenantID and Role always come
// from the authenticated session.
type ListFeedbackReportCounts struct {
	TenantID string
	Role     string
	// CanView is true when the session holds feedback.view (custom roles).
	CanView bool
	From    *time.Time
	To      *time.Time
}

// FeedbackReportCounts is the denominator of "Reports rated" on the Model
// Feedback page: completed executions per model version and modality.
type FeedbackReportCounts struct {
	From   time.Time                  `json:"from"`
	To     time.Time                  `json:"to"`
	Models []FeedbackReportCountModel `json:"models"`
}

// FeedbackReportCountModel counts the completed executions of one model
// version and modality.
type FeedbackReportCountModel struct {
	ModelName      string `json:"modelName"`
	ModelVersion   string `json:"modelVersion"`
	Modality       string `json:"modality"`
	CompletedCount int    `json:"completedCount"`
}
