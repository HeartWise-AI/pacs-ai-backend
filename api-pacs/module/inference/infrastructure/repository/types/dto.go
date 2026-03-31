package types

import (
	"api-pacs/module/inference/domain/entity"
	"time"
)

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

type CreateInferenceIngestionJob struct {
	ID                     string
	TenantID               string
	DICOMModality          string
	ContainerID            string
	ModelID                string
	ModelName              string
	ModelVersion           string
	Modalities             []string
	IntervalInMinutes      uint
	ScheduleStartTimestamp time.Time
	ScheduleEndTimestamp   time.Time
	Status                 entity.InferenceIngestionJobStatus
}

type GetModelFeedbackByUserModelID struct {
	TenantID string
	UserID   string
	ModelID  string
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

type UpdateInferenceIngestionJob struct {
	ID                     string
	Modalities             []string
	IntervalInMinutes      uint
	ScheduleStartTimestamp time.Time
	ScheduleEndTimestamp   time.Time
}

type UpsertModelFeedback struct {
	ID               string
	TenantID         string
	UserID           string
	InferenceModelID string
	ModelID          string
	FeedbackType     entity.FeedbackType
}
