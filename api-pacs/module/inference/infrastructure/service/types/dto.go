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
	StabilityMinutes       uint
	RecentWindowMinutes    uint
	MissingPollsThreshold  uint
	StudyTimeStart         string
	StudyTimeEnd           string
	ScheduleStartTimestamp uint64
	ScheduleEndTimestamp   uint64
}

type BuildStudyServiceDispatchRequestInput struct {
	IngestionJob       entity.InferenceIngestionJob
	Candidate          entity.InferenceIngestionCandidate
	OrthancStudyID     *string
	RetrievalAttemptID *string
	RequestID          *string
}

type DispatchStudyRequest struct {
	XRequestID         string  `json:"-"`
	TenantID           *string `json:"tenant_id"`
	IngestionJobID     *string `json:"ingestion_job_id"`
	CandidateID        *string `json:"candidate_id"`
	RetrievalAttemptID *string `json:"retrieval_attempt_id"`
	StudyInstanceUID   string  `json:"study_instance_uid"`
	OrthancStudyID     string  `json:"orthanc_study_id"`
	Modality           string  `json:"modality"`
	ModelName          string  `json:"model_name"`
	ModelVersion       string  `json:"model_version"`
}

type DispatchStudyResponse struct {
	JobID          string `json:"job_id"`
	AlreadyPresent bool   `json:"already_present"`
	StatusCode     int    `json:"-"`
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

type GetInferenceIngestionCandidates struct {
	TenantID           string
	IngestionJobID     *string
	StudyInstanceUID   *string
	Status             *string
	RetrievalFailures  bool
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
	StabilityMinutes       uint
	RecentWindowMinutes    uint
	MissingPollsThreshold  uint
	StudyTimeStart         string
	StudyTimeEnd           string
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
