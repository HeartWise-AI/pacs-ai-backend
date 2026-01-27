package types

import "api-pacs/module/inference/domain/entity"

type AddInferenceModel struct {
	ID                  string
	TenantID            string
	ContainerID         string
	Name                string
	DockerImage         string
	Envs                []string
	DisallowedDICOMTags []string
	OutputMode          entity.OutputMode
}

type AddModelFeedbackAnswer struct {
	ID                     string
	ModelFeedbackID        string
	QuestionnaireID        string
	QuestionnaireQuestions []string
	QuestionnaireAnswers   []string
}

type UpdateInferenceModel struct {
	ID                  string
	DisallowedDICOMTags []string
	OutputMode          entity.OutputMode
}

type UpsertModelFeedback struct {
	ID           string
	TenantID     string
	UserID       string
	ModelID      string
	FeedbackType entity.FeedbackType
}
