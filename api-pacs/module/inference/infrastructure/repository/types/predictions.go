package types

import (
	"time"

	"api-pacs/module/inference/domain/entity"
)

// PredictionsScope selects the exams of the AI Predictions page.
type PredictionsScope string

const (
	// PredictionsScopeRequestedBy keeps exams where RequestedByUserID launched at least one run.
	PredictionsScopeRequestedBy PredictionsScope = "requested_by"
	// PredictionsScopeAutomatic keeps exams with at least one automatic run.
	PredictionsScopeAutomatic PredictionsScope = "automatic"
	// PredictionsScopePeople keeps exams with at least one run a user launched.
	PredictionsScopePeople PredictionsScope = "people"
	// PredictionsScopeAll keeps every exam of the tenant.
	PredictionsScopeAll PredictionsScope = "all"
)

// PredictionsExamStatus filters exams on the latest state of their models.
type PredictionsExamStatus string

const (
	PredictionsExamStatusAny PredictionsExamStatus = ""
	// PredictionsExamStatusActive: at least one model is waiting, queued or
	// running and not stalled.
	PredictionsExamStatusActive PredictionsExamStatus = "active"
	// PredictionsExamStatusAttention: at least one model failed, was skipped
	// or stalled (never ran). Expired models (closed by the expiry of
	// automatic runs) do not need attention.
	PredictionsExamStatusAttention PredictionsExamStatus = "attention"
	// PredictionsExamStatusCompleted: every model completed.
	PredictionsExamStatusCompleted PredictionsExamStatus = "completed"
)

// ListPredictionExams is one tenant-scoped page request of the AI Predictions
// exam list. Every value is bound as a query parameter.
type ListPredictionExams struct {
	TenantID string
	Scope    PredictionsScope
	// RequestedByUserID is required by PredictionsScopeRequestedBy.
	RequestedByUserID string
	// ModelName keeps exams that ran this model; the latest state, the
	// activity time and the status filter then consider this model only.
	ModelName string
	Status    PredictionsExamStatus
	// Modality is a DICOM modality matched against the exam's modalities
	// and the modality the models were dispatched with.
	Modality string
	// Search matches the patient ID or the accession number (contains,
	// case-insensitive) or the exact Study Instance UID.
	Search string
	// From and To bound the exam's last activity (inclusive, exclusive).
	From *time.Time
	To   *time.Time
	// StalledBefore: a pending or queued execution without a study-service
	// job whose run was created before this time never ran (stalled). The
	// zero time marks nothing as stalled.
	StalledBefore time.Time
	Limit         int
	Offset        int
}

// PredictionExamRow is one exam of a page with the metadata the gateway
// stored for it. Patient names are not stored.
type PredictionExamRow struct {
	StudyInstanceUID  string    `db:"study_instance_uid"`
	LastActivityAt    time.Time `db:"last_activity_at"`
	PatientID         *string   `db:"patient_id"`
	AccessionNumber   *string   `db:"accession_number"`
	ModalitiesInStudy *string   `db:"modalities_in_study"`
	StudyDate         *string   `db:"study_date"`
	Total             int       `db:"total"`
}

// PredictionExamPage is a page of exams and the number of matching exams.
type PredictionExamPage struct {
	Exams []PredictionExamRow
	Total int
}

// PredictionExecutionRow is one model execution with its run and the
// ingestion state of the exam it waits for.
type PredictionExecutionRow struct {
	StudyInstanceUID    string                                                 `db:"study_instance_uid"`
	RunID               string                                                 `db:"run_id"`
	RunNumber           int                                                    `db:"run_number"`
	RunTrigger          entity.InferenceIngestionProcessingRunTrigger          `db:"run_trigger"`
	RunPhase            entity.InferenceIngestionProcessingRunPhase            `db:"run_phase"`
	RunAttentionReasons entity.InferenceIngestionProcessingRunAttentionReasons `db:"run_attention_reasons"`
	RequestedByUserID   *string                                                `db:"requested_by_user_id"`
	RunCreatedAt        time.Time                                              `db:"run_created_at"`
	CancelledAt         *time.Time                                             `db:"cancelled_at"`
	ExecutionID         string                                                 `db:"execution_id"`
	ModelName           string                                                 `db:"model_name"`
	ModelVersion        *string                                                `db:"model_version"`
	Modality            *string                                                `db:"modality"`
	Status              entity.InferenceIngestionProcessingJobStatus           `db:"status"`
	StudyServiceJobID   *string                                                `db:"study_service_job_id"`
	ErrorMessage        *string                                                `db:"error_message"`
	SkipReasonCode      *entity.InferenceIngestionProcessingJobSkipReasonCode  `db:"skip_reason_code"`
	SkipReasonMessage   *string                                                `db:"skip_reason_message"`
	StartedAt           *time.Time                                             `db:"started_at"`
	CompletedAt         *time.Time                                             `db:"completed_at"`
	CreatedAt           time.Time                                              `db:"created_at"`
	UpdatedAt           time.Time                                              `db:"updated_at"`
	CandidateStatus     *entity.InferenceIngestionCandidateStatus              `db:"candidate_status"`
	// EarlierRuns counts the older executions of the same model on the exam;
	// it is filled by the latest-per-model query only.
	EarlierRuns int `db:"earlier_runs"`
}

// ListPredictionExecutions selects executions of a set of exams.
type ListPredictionExecutions struct {
	TenantID          string
	StudyInstanceUIDs []string
	// ModelName optionally keeps one model.
	ModelName string
	// LatestOnly keeps the newest execution of each model of each exam.
	LatestOnly bool
	// LaunchedBy optionally keeps the runs of one scope (export lines);
	// PredictionsScopeRequestedBy uses RequestedByUserID.
	LaunchedBy        PredictionsScope
	RequestedByUserID string
}

// ListPredictionModelHistory pages the executions of one model on one exam, newest first.
type ListPredictionModelHistory struct {
	TenantID         string
	StudyInstanceUID string
	ModelName        string
	Limit            int
	Offset           int
}

// PredictionLauncherRow counts the runs of one exam by trigger and requester.
type PredictionLauncherRow struct {
	StudyInstanceUID  string                                        `db:"study_instance_uid"`
	RunTrigger        entity.InferenceIngestionProcessingRunTrigger `db:"run_trigger"`
	RequestedByUserID *string                                       `db:"requested_by_user_id"`
	Runs              int                                           `db:"runs"`
	FirstAt           time.Time                                     `db:"first_at"`
}
