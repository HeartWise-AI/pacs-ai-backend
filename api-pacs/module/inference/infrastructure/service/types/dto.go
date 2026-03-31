package types

import (
	"time"

	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	"api-pacs/module/inference/domain/entity"
)

type AddInferenceModel struct {
	TenantID    string
	Name        string
	DockerImage string
	Envs        []string
	OutputMode  entity.OutputMode
}

type AddOnboardingModelQuestionnaireAnswer struct {
	ID                                  string
	TenantID                            string
	UserID                              string
	ModelID                             string
	OnboardingModelQuestionnaireAnswers []OnboardingModelQuestionnaireAnswer
}

type CreateInferenceIngestionJob struct {
	TenantID               string
	DICOMModality          string
	ContainerID            string
	ModelID                string
	ModelName              string
	ModelVersion           string
	Modalities             []string
	IntervalInMinutes      uint
	ScheduleStartTimestamp uint64
	ScheduleEndTimestamp   uint64
}

type GetContainerInfoResult struct {
	ID              string
	Name            string
	Status          dockerTypes.Status
	Running         bool
	StartedAt       time.Time
	FinishedAt      time.Time
	CPUPercentUsage float64 // in percent
	MemoryInBytes   uint64  // in bytes
}

type GetInferenceModelResult struct {
	ID                  string
	TenantID            string
	Container           GetContainerInfoResult
	Name                string
	DockerImage         string
	DisallowedDICOMTags []string
	Envs                []string
	OutputMode          entity.OutputMode
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type GetInferenceAvailableModelResult struct {
	ContainerID                   string
	ContainerName                 string
	ModelID                       string
	ModelName                     string
	ModelFacts                    ModelFacts
	Version                       string
	DicomTargetLevel              string
	DicomUploadMin                int
	DicomUploadMax                int
	SupportedDicomModalities      []string
	SupportedDicomTags            []string
	SupportedAdditionalMetadata   []interface{}
	ApproveFeedbackQuestionnaires []interface{}
	RejectFeedbackQuestionnaires  []interface{}
	OnboardingModelQuestionnaires []interface{}
	OutputMode                    entity.OutputMode
}

type GetModelFeedbackByUser struct {
	TenantID string
	UserID   string
	ModelID  string
}

type GetOnboardingModelQuestionnaireAnswer struct {
	TenantID string
	UserID   string
	ModelID  *string
}

type GetModelFeedbackResult struct {
	ID                   string
	TenantID             string
	UserID               string
	InferenceModelID     string
	ModelID              string
	FeedbackType         entity.FeedbackType
	ModelFeedbackAnswers []ModelFeedbackAnswerResult
}

type PredictInferenceModel struct {
	StudyInstanceUID   string
	SeriesInstanceUIDs []string
	AdditionalMetadata map[string]interface{}
	ForceJSON          *bool
}

type RemoveModelFeedback struct {
	TenantID string
	UserID   string
	ModelID  string
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
	ScheduleStartTimestamp uint64
	ScheduleEndTimestamp   uint64
}

type UpdateModelFeedback struct {
	ID                   *string
	TenantID             string
	UserID               string
	InferenceModelID     string
	ModelID              string
	FeedbackType         entity.FeedbackType
	ModelFeedbackAnswers []ModelFeedbackAnswer
}

type ModelFacts struct {
	En map[string]interface{}
}

type ModelFeedbackAnswer struct {
	ID                     string
	ModelFeedbackID        *string
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type ModelFeedbackAnswerResult struct {
	ID                     string
	ModelFeedbackID        string
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}

type OnboardingModelQuestionnaireAnswer struct {
	QuestionnaireID        string
	QuestionnaireQuestion  string
	QuestionnaireAnswerIDs []string
	QuestionnaireAnswers   []string
}
