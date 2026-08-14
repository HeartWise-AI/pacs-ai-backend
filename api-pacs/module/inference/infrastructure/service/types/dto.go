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

type CreateStudyProcessingRun struct {
	TenantID         string
	StudyInstanceUID string
	UserID           *string
}

type CreateStudyProcessingRunResult struct {
	Run        entity.InferenceIngestionProcessingRun
	Executions []entity.InferenceIngestionProcessingJob
	Created    bool
}

type RecalculateStudyProcessingRun struct {
	TenantID                     string
	ProcessingRunID              string
	WholeRunCancelled            bool
	AttentionReasonsToAdd        entity.InferenceIngestionProcessingRunAttentionReasons
	AttentionReasonCodesToRemove []string
}

type RecalculateStudyProcessingRunResult struct {
	Run    entity.InferenceIngestionProcessingRun
	Counts entity.InferenceIngestionProcessingRunExecutionCounts
}

type DispatchStudyIntent string

const (
	DispatchStudyIntentAutomatic       DispatchStudyIntent = "automatic"
	DispatchStudyIntentManualReprocess DispatchStudyIntent = "manual_reprocess"
)

type BuildStudyServiceDispatchRequestInput struct {
	IngestionJob          entity.InferenceIngestionJob
	Candidate             entity.InferenceIngestionCandidate
	OrthancStudyID        *string
	RetrievalAttemptID    *string
	ProcessingRunID       *string
	ProcessingExecutionID *string
	DispatchIntent        DispatchStudyIntent
	RequestID             *string
}

type DispatchStudyRequest struct {
	XRequestID            string              `json:"-"`
	TenantID              *string             `json:"tenant_id"`
	IngestionJobID        *string             `json:"ingestion_job_id"`
	CandidateID           *string             `json:"candidate_id"`
	RetrievalAttemptID    *string             `json:"retrieval_attempt_id"`
	ProcessingRunID       *string             `json:"processing_run_id,omitempty"`
	ProcessingExecutionID *string             `json:"processing_execution_id,omitempty"`
	DispatchIntent        DispatchStudyIntent `json:"dispatch_intent,omitempty"`
	StudyInstanceUID      string              `json:"study_instance_uid"`
	OrthancStudyID        string              `json:"orthanc_study_id"`
	Modality              string              `json:"modality"`
	ModelName             string              `json:"model_name"`
	ModelVersion          string              `json:"model_version"`
}

type DispatchStudyResponse struct {
	JobID                 string  `json:"job_id"`
	AlreadyPresent        bool    `json:"already_present"`
	ProcessingRunID       *string `json:"processing_run_id"`
	ProcessingExecutionID *string `json:"processing_execution_id"`
	RerunOf               *string `json:"rerun_of"`
	StatusCode            int     `json:"-"`
}

type StudyServiceJob struct {
	JobID              string     `json:"job_id"`
	StudyInstanceUID   string     `json:"study_instance_uid"`
	PatientID          string     `json:"patient_id"`
	TenantID           *string    `json:"tenant_id"`
	IngestionJobID     *string    `json:"ingestion_job_id"`
	CandidateID        *string    `json:"candidate_id"`
	RetrievalAttemptID *string    `json:"retrieval_attempt_id"`
	ProcessingRunID    *string    `json:"processing_run_id"`
	Modality           string     `json:"modality"`
	ModelName          string     `json:"model_name"`
	ModelVersion       *string    `json:"model_version"`
	Status             string     `json:"status"`
	ErrorMessage       *string    `json:"error_message"`
	CreatedAt          *time.Time `json:"created_at"`
	StartedAt          *time.Time `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at"`
}

type StudyServiceJobsResponse struct {
	Jobs     []StudyServiceJob `json:"jobs"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

type StudyServiceCallbackDeadLetterPayload struct {
	CandidateID     string `json:"candidate_id"`
	ProcessingRunID string `json:"processing_run_id"`
	ModelName       string `json:"model_name"`
}

type StudyServiceCallbackDeadLetter struct {
	DeadLetterID string                                `json:"dead_letter_id"`
	JobID        string                                `json:"job_id"`
	CandidateID  *string                               `json:"candidate_id"`
	JobStatus    string                                `json:"job_status"`
	Payload      StudyServiceCallbackDeadLetterPayload `json:"payload_json"`
	Attempts     int                                   `json:"attempts"`
	LastError    string                                `json:"last_error"`
}

type StudyServiceCallbackDeadLettersResponse struct {
	DeadLetters []StudyServiceCallbackDeadLetter `json:"dead_letters"`
}

type HandleStudyServiceProcessingCallback struct {
	CandidateID        string
	RequestID          string
	EventID            string
	Sequence           *int64
	OccurredAt         *time.Time
	TenantID           string
	IngestionJobID     string
	PayloadCandidateID string
	RetrievalAttemptID string
	ProcessingRunID    string
	StudyInstanceUID   string
	ModelName          string
	ModelVersion       string
	Modality           string
	Status             string
	SkipReason         *entity.InferenceIngestionProcessingJobSkipReason
	ErrorMessage       *string
	StudyServiceJobID  string
	StartedAt          *time.Time
	CompletedAt        *time.Time
}

type HandleStudyServiceProcessingCallbackResult struct {
	Outcome string
}

const WorklistNotificationTypeStudyStatusUpdated = "study_status.updated"

// WorklistNotification is the versioned aggregate published after a processing
// run change commits. TenantID is routing metadata for the broker and must
// never be exposed to the browser.
type WorklistNotification struct {
	Type              string                                                 `json:"type"`
	TenantID          string                                                 `json:"-"`
	StudyInstanceUID  string                                                 `json:"studyInstanceUID"`
	RunID             string                                                 `json:"runId"`
	RunNumber         int                                                    `json:"runNumber"`
	Trigger           entity.InferenceIngestionProcessingRunTrigger          `json:"trigger"`
	Phase             entity.InferenceIngestionProcessingRunPhase            `json:"phase"`
	Outcome           *entity.InferenceIngestionProcessingRunOutcome         `json:"outcome"`
	AttentionRequired bool                                                   `json:"attentionRequired"`
	AttentionReasons  entity.InferenceIngestionProcessingRunAttentionReasons `json:"attentionReasons"`
	ExpectedModels    int                                                    `json:"expectedModels"`
	PendingModels     int                                                    `json:"pendingModels"`
	QueuedModels      int                                                    `json:"queuedModels"`
	RunningModels     int                                                    `json:"runningModels"`
	CompletedModels   int                                                    `json:"completedModels"`
	FailedModels      int                                                    `json:"failedModels"`
	SkippedModels     int                                                    `json:"skippedModels"`
	CancelledModels   int                                                    `json:"cancelledModels"`
	ActiveModels      int                                                    `json:"activeModels"`
	Version           int64                                                  `json:"version"`
	StartedAt         *time.Time                                             `json:"startedAt"`
	CompletedAt       *time.Time                                             `json:"completedAt"`
	UpdatedAt         time.Time                                              `json:"updatedAt"`
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
	TenantID          string
	IngestionJobID    *string
	StudyInstanceUID  *string
	Status            *string
	RetrievalFailures bool
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
