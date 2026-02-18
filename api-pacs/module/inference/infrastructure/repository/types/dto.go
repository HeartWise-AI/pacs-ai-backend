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
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type AddOnboardingQuestionnaireAnswer struct {
	ID                     string
	TenantID               string
	UserID                 string
	QuestionnaireType      entity.QuestionnaireType
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type AddOnboardingModelQuestionnaireAnswer struct {
	ID                     string
	TenantID               string
	UserID                 string
	ModelID                string
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type GetModelFeedbackByUserModelID struct {
	TenantID string
	UserID   string
	ModelID  string
}

type GetOnboardingQuestionnaireAnswer struct {
	TenantID          string
	UserID            string
	QuestionnaireType *entity.QuestionnaireType
}

type GetOnboardingModelQuestionnaireAnswer struct {
	TenantID string
	UserID   string
	ModelID  *string
}

type UpdateInferenceModel struct {
	ID                  string
	DisallowedDICOMTags []string
	OutputMode          entity.OutputMode
}

type UpsertModelFeedback struct {
	ID               string
	TenantID         string
	UserID           string
	InferenceModelID string
	ModelID          string
	FeedbackType     entity.FeedbackType
}
