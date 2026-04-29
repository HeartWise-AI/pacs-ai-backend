package types

import (
	"time"

	"api-pacs/module/inference/domain/entity"
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

type AddInferenceIngestionProcessingJob struct {
	ID                string
	CandidateID       string
	TenantID          string
	ModelName         string
	ModelVersion      *string
	Modality          *string
	Status            entity.InferenceIngestionProcessingJobStatus
	StudyServiceJobID *string
	ErrorMessage      *string
	StartedAt         *time.Time
	CompletedAt       *time.Time
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
	StabilityMinutes       uint
	RecentWindowMinutes    uint
	MissingPollsThreshold  uint
	StudyTimeStart         *string
	StudyTimeEnd           *string
	ScheduleStartTimestamp time.Time
	ScheduleEndTimestamp   time.Time
	Status                 entity.InferenceIngestionJobStatus
}

type GetModelFeedbackByUserModelID struct {
	TenantID string
	UserID   string
	ModelID  string
}

type ListInferenceIngestionCandidates struct {
	TenantID          string
	IngestionJobID    *string
	StudyInstanceUID  *string
	Status            *entity.InferenceIngestionCandidateStatus
	RetrievalFailures bool
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
	StabilityMinutes       uint
	RecentWindowMinutes    uint
	MissingPollsThreshold  uint
	StudyTimeStart         *string
	StudyTimeEnd           *string
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

type UpsertIngestionCandidate struct {
	ID                string
	TenantID          string
	IngestionJobID    string
	StudyInstanceUID  string
	StudyDate         *string
	StudyTime         *string
	ModalitiesInStudy *string
	PatientID         *string
	AccessionNumber   *string
	SeriesCount       *int
	InstanceCount     *int
}

type UpdateCandidateRetrievalState struct {
	ID                        string
	OrthancJobIDs             []string
	LastRetrievalState        *string
	LastRetrievalError        *string
	LastRetrievalErrorDetails *string
}
