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
	LastEventID       *string
	LastEventSequence *int64
	StartedAt         *time.Time
	CompletedAt       *time.Time
}

// CreateInferenceIngestionProcessingRun contains immutable values supplied when a run is created.
// The repository allocates RunNumber and initializes the aggregate version transactionally.
type CreateInferenceIngestionProcessingRun struct {
	ID               string
	TenantID         string                                        `db:"tenant_id"`
	StudyInstanceUID string                                        `db:"study_instance_uid"`
	RunTrigger       entity.InferenceIngestionProcessingRunTrigger `db:"run_trigger"`
	Phase            entity.InferenceIngestionProcessingRunPhase   `db:"phase"`
}

// CreateInferenceIngestionProcessingExecution contains one frozen expected-model execution.
type CreateInferenceIngestionProcessingExecution struct {
	ID           string
	CandidateID  string
	ModelName    string
	ModelVersion *string
	Modality     *string
}

// CreateInferenceIngestionProcessingRunPlan creates a run and its expected executions as one unit.
type CreateInferenceIngestionProcessingRunPlan struct {
	Run        CreateInferenceIngestionProcessingRun
	Executions []CreateInferenceIngestionProcessingExecution
}

// CreateInferenceIngestionProcessingRunPlanResult reports whether a plan was created or reused.
type CreateInferenceIngestionProcessingRunPlanResult struct {
	Run        entity.InferenceIngestionProcessingRun
	Executions []entity.InferenceIngestionProcessingJob
	Created    bool
}

// ListInferenceIngestionProcessingRuns scopes a paginated study run-history query.
type ListInferenceIngestionProcessingRuns struct {
	TenantID         string `db:"tenant_id"`
	StudyInstanceUID string `db:"study_instance_uid"`
	Limit            int    `db:"limit"`
	Offset           int    `db:"offset"`
}

// InferenceIngestionProcessingRunHistoryPage carries limit+1 pagination
// information without requiring a separate total-count query.
type InferenceIngestionProcessingRunHistoryPage struct {
	Runs    []entity.InferenceIngestionProcessingRun
	HasMore bool
}

// ListInferenceIngestionProcessingRunExecutions scopes a batch execution read
// to one tenant and the processing runs visible on the requested history page.
type ListInferenceIngestionProcessingRunExecutions struct {
	TenantID         string
	ProcessingRunIDs []string
}

// ListInferenceIngestionProcessingRunsForReconciliation bounds the internal
// cross-tenant worker query. ActiveStaleBefore is the earliest configured
// threshold; state-specific thresholds are applied by the service.
type ListInferenceIngestionProcessingRunsForReconciliation struct {
	ActiveStaleBefore time.Time `db:"active_stale_before"`
	Limit             int       `db:"limit"`
}

// RecordInferenceIngestionProcessingRunReconciliationAttempt updates durable
// worker health without relying on process-local counters.
type RecordInferenceIngestionProcessingRunReconciliationAttempt struct {
	ID          string    `db:"id"`
	TenantID    string    `db:"tenant_id"`
	Succeeded   bool      `db:"succeeded"`
	AttemptedAt time.Time `db:"attempted_at"`
}

// UpdateInferenceIngestionProcessingRunAggregate applies one optimistic aggregate transition.
type UpdateInferenceIngestionProcessingRunAggregate struct {
	ID                string
	TenantID          string                                                 `db:"tenant_id"`
	ExpectedVersion   int64                                                  `db:"expected_version"`
	Phase             entity.InferenceIngestionProcessingRunPhase            `db:"phase"`
	Outcome           *entity.InferenceIngestionProcessingRunOutcome         `db:"outcome"`
	AttentionRequired bool                                                   `db:"attention_required"`
	AttentionReasons  entity.InferenceIngestionProcessingRunAttentionReasons `db:"attention_reasons"`
	StartedAt         *time.Time                                             `db:"started_at"`
	CompletedAt       *time.Time                                             `db:"completed_at"`
}

type ApplyInferenceIngestionProcessingTransition struct {
	TenantID          string
	ProcessingRunID   string
	ExecutionID       string
	CandidateID       string
	ModelName         string
	Status            entity.InferenceIngestionProcessingJobStatus
	ModelVersion      *string
	Modality          *string
	StudyServiceJobID *string
	ErrorMessage      *string
	SkipReason        *entity.InferenceIngestionProcessingJobSkipReason
	EventID           *string
	EventSequence     *int64
	StartedAt         *time.Time
	CompletedAt       *time.Time
}

type ApplyInferenceIngestionProcessingTransitionResult struct {
	Outcome   string
	Changed   bool
	Execution entity.InferenceIngestionProcessingJob
	Run       entity.InferenceIngestionProcessingRun
	Counts    entity.InferenceIngestionProcessingRunExecutionCounts
}

// LegacyProcessingRunBackfillRow is the read-only projection used to plan
// LEGACY_IMPORT runs without loading patient, DICOM, or inference-result data.
type LegacyProcessingRunBackfillRow struct {
	ExecutionID       string                                       `db:"execution_id"`
	CandidateID       string                                       `db:"candidate_id"`
	ExecutionTenantID string                                       `db:"execution_tenant_id"`
	CandidateTenantID string                                       `db:"candidate_tenant_id"`
	StudyInstanceUID  string                                       `db:"study_instance_uid"`
	ModelName         string                                       `db:"model_name"`
	Status            entity.InferenceIngestionProcessingJobStatus `db:"status"`
	ExistingRun       bool                                         `db:"existing_run"`
}

// ImportLegacyProcessingRun identifies one preflight-approved logical study.
type ImportLegacyProcessingRun struct {
	RunID            string
	TenantID         string
	StudyInstanceUID string
}

// ImportLegacyProcessingRunResult returns the committed legacy aggregate and
// linked execution count without exposing detailed inference results.
type ImportLegacyProcessingRunResult struct {
	Run              entity.InferenceIngestionProcessingRun
	Counts           entity.InferenceIngestionProcessingRunExecutionCounts
	LinkedExecutions int
}

type UpdateInferenceIngestionProcessingJob struct {
	ID                string
	Status            entity.InferenceIngestionProcessingJobStatus
	ModelVersion      *string
	Modality          *string
	StudyServiceJobID *string
	ErrorMessage      *string
	LastEventID       *string
	LastEventSequence *int64
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

// ListWorklistStudyStatuses scopes the current worklist snapshot to one tenant.
// StudyInstanceUIDs is optional and lets callers restrict the database query to
// the studies visible on the current worklist page.
type ListWorklistStudyStatuses struct {
	TenantID          string
	StudyInstanceUIDs []string
	Limit             int
	Offset            int
}

// WorklistStudyStatus is the repository projection for one logical study and
// its active run, or its latest terminal run when no active run exists.
type WorklistStudyStatus struct {
	StudyInstanceUID  string                                                 `db:"study_instance_uid"`
	IngestionStatus   entity.InferenceIngestionCandidateStatus               `db:"ingestion_status"`
	RetrievalState    *string                                                `db:"retrieval_state"`
	RetrievalError    *string                                                `db:"retrieval_error"`
	RunID             *string                                                `db:"run_id"`
	RunNumber         *int                                                   `db:"run_number"`
	RunTrigger        *entity.InferenceIngestionProcessingRunTrigger         `db:"run_trigger"`
	Phase             *entity.InferenceIngestionProcessingRunPhase           `db:"phase"`
	Outcome           *entity.InferenceIngestionProcessingRunOutcome         `db:"outcome"`
	AttentionRequired bool                                                   `db:"attention_required"`
	AttentionReasons  entity.InferenceIngestionProcessingRunAttentionReasons `db:"attention_reasons"`
	ExpectedModels    int                                                    `db:"expected_models"`
	PendingModels     int                                                    `db:"pending_models"`
	QueuedModels      int                                                    `db:"queued_models"`
	RunningModels     int                                                    `db:"running_models"`
	CompletedModels   int                                                    `db:"completed_models"`
	FailedModels      int                                                    `db:"failed_models"`
	SkippedModels     int                                                    `db:"skipped_models"`
	CancelledModels   int                                                    `db:"cancelled_models"`
	ActiveModels      int                                                    `db:"active_models"`
	Version           *int64                                                 `db:"version"`
	StartedAt         *time.Time                                             `db:"started_at"`
	CompletedAt       *time.Time                                             `db:"completed_at"`
	UpdatedAt         time.Time                                              `db:"updated_at"`
}

// WorklistStudyStatusPage carries limit+1 pagination information without a
// separate count query.
type WorklistStudyStatusPage struct {
	Studies []WorklistStudyStatus
	HasMore bool
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

type UpdateCandidateDispatchState struct {
	ID                      string
	LastDispatchError       *string
	LastDispatchAttemptedAt *time.Time
}
