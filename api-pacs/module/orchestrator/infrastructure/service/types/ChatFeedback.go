package types

// ChatFeedbackVote is the body of a vote on one chat answer.
type ChatFeedbackVote struct {
	Vote     string  `json:"vote"`
	Category string  `json:"category"`
	Comment  *string `json:"comment,omitempty"`
	Client   string  `json:"client,omitempty"`
}
