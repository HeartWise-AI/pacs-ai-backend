package entity

import (
	"fmt"
	"strings"
	"time"
)

type InferenceIngestionProcessingJobStatus string
type InferenceIngestionProcessingJobSkipReasonCode string

const (
	InferenceIngestionProcessingJobStatusPending   InferenceIngestionProcessingJobStatus = "pending"
	InferenceIngestionProcessingJobStatusQueued    InferenceIngestionProcessingJobStatus = "queued"
	InferenceIngestionProcessingJobStatusRunning   InferenceIngestionProcessingJobStatus = "running"
	InferenceIngestionProcessingJobStatusCompleted InferenceIngestionProcessingJobStatus = "completed"
	InferenceIngestionProcessingJobStatusFailed    InferenceIngestionProcessingJobStatus = "failed"
	InferenceIngestionProcessingJobStatusSkipped   InferenceIngestionProcessingJobStatus = "skipped"
	InferenceIngestionProcessingJobStatusCancelled InferenceIngestionProcessingJobStatus = "cancelled"
)

// IsValid reports whether the status is part of the processing-execution contract.
func (status InferenceIngestionProcessingJobStatus) IsValid() bool {
	switch status {
	case InferenceIngestionProcessingJobStatusPending,
		InferenceIngestionProcessingJobStatusQueued,
		InferenceIngestionProcessingJobStatusRunning,
		InferenceIngestionProcessingJobStatusCompleted,
		InferenceIngestionProcessingJobStatusFailed,
		InferenceIngestionProcessingJobStatusSkipped,
		InferenceIngestionProcessingJobStatusCancelled:
		return true
	default:
		return false
	}
}

// ParseInferenceIngestionProcessingJobStatus normalizes an external status and validates it.
func ParseInferenceIngestionProcessingJobStatus(value string) (InferenceIngestionProcessingJobStatus, bool) {
	status := InferenceIngestionProcessingJobStatus(strings.ToLower(strings.TrimSpace(value)))
	return status, status.IsValid()
}

// IsTerminal reports whether no later non-replayed transition is allowed.
func (status InferenceIngestionProcessingJobStatus) IsTerminal() bool {
	switch status {
	case InferenceIngestionProcessingJobStatusCompleted,
		InferenceIngestionProcessingJobStatusFailed,
		InferenceIngestionProcessingJobStatusSkipped,
		InferenceIngestionProcessingJobStatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransitionTo applies the authoritative execution-state machine.
// Equal valid statuses are accepted as idempotent replays.
func (status InferenceIngestionProcessingJobStatus) CanTransitionTo(next InferenceIngestionProcessingJobStatus) bool {
	if !status.IsValid() || !next.IsValid() {
		return false
	}
	if status == next {
		return true
	}

	// Terminal callbacks are authoritative and may arrive without the optional running event.
	switch status {
	case InferenceIngestionProcessingJobStatusPending:
		return next == InferenceIngestionProcessingJobStatusQueued ||
			next == InferenceIngestionProcessingJobStatusCompleted ||
			next == InferenceIngestionProcessingJobStatusSkipped ||
			next == InferenceIngestionProcessingJobStatusFailed ||
			next == InferenceIngestionProcessingJobStatusCancelled
	case InferenceIngestionProcessingJobStatusQueued:
		return next == InferenceIngestionProcessingJobStatusRunning ||
			next == InferenceIngestionProcessingJobStatusCompleted ||
			next == InferenceIngestionProcessingJobStatusFailed ||
			next == InferenceIngestionProcessingJobStatusCancelled ||
			next == InferenceIngestionProcessingJobStatusSkipped
	case InferenceIngestionProcessingJobStatusRunning:
		return next == InferenceIngestionProcessingJobStatusCompleted ||
			next == InferenceIngestionProcessingJobStatusFailed ||
			next == InferenceIngestionProcessingJobStatusCancelled ||
			next == InferenceIngestionProcessingJobStatusSkipped
	default:
		return false
	}
}

const (
	InferenceIngestionProcessingJobSkipReasonNoUsableDICOM         InferenceIngestionProcessingJobSkipReasonCode = "NO_USABLE_DICOM"
	InferenceIngestionProcessingJobSkipReasonUnsupportedModality   InferenceIngestionProcessingJobSkipReasonCode = "UNSUPPORTED_MODALITY"
	InferenceIngestionProcessingJobSkipReasonRequiredSeriesMissing InferenceIngestionProcessingJobSkipReasonCode = "REQUIRED_SERIES_MISSING"
	InferenceIngestionProcessingJobSkipReasonModelNotApplicable    InferenceIngestionProcessingJobSkipReasonCode = "MODEL_NOT_APPLICABLE"
	InferenceIngestionProcessingJobSkipReasonPrerequisiteNotMet    InferenceIngestionProcessingJobSkipReasonCode = "PREREQUISITE_NOT_MET"
	InferenceIngestionProcessingJobSkipReasonModelDisabled         InferenceIngestionProcessingJobSkipReasonCode = "MODEL_DISABLED"
)

// InferenceIngestionProcessingJobSkipReason describes why an expected model did not run.
type InferenceIngestionProcessingJobSkipReason struct {
	Code    InferenceIngestionProcessingJobSkipReasonCode `json:"code"`
	Message *string                                       `json:"message,omitempty"`
}

// IsValid reports whether the code is part of the supported skip-reason contract.
func (code InferenceIngestionProcessingJobSkipReasonCode) IsValid() bool {
	switch code {
	case InferenceIngestionProcessingJobSkipReasonNoUsableDICOM,
		InferenceIngestionProcessingJobSkipReasonUnsupportedModality,
		InferenceIngestionProcessingJobSkipReasonRequiredSeriesMissing,
		InferenceIngestionProcessingJobSkipReasonModelNotApplicable,
		InferenceIngestionProcessingJobSkipReasonPrerequisiteNotMet,
		InferenceIngestionProcessingJobSkipReasonModelDisabled:
		return true
	default:
		return false
	}
}

// ParseInferenceIngestionProcessingJobSkipReasonCode normalizes an external code and validates it.
func ParseInferenceIngestionProcessingJobSkipReasonCode(value string) (InferenceIngestionProcessingJobSkipReasonCode, bool) {
	code := InferenceIngestionProcessingJobSkipReasonCode(strings.ToUpper(strings.TrimSpace(value)))
	return code, code.IsValid()
}

// NewInferenceIngestionProcessingJobSkipReason creates a validated structured skip reason.
func NewInferenceIngestionProcessingJobSkipReason(codeValue string, message *string) (InferenceIngestionProcessingJobSkipReason, error) {
	code, ok := ParseInferenceIngestionProcessingJobSkipReasonCode(codeValue)
	if !ok {
		return InferenceIngestionProcessingJobSkipReason{}, fmt.Errorf("invalid processing job skip reason code %q", strings.TrimSpace(codeValue))
	}

	var normalizedMessage *string
	if message != nil {
		trimmed := strings.TrimSpace(*message)
		if trimmed != "" {
			normalizedMessage = &trimmed
		}
	}
	return InferenceIngestionProcessingJobSkipReason{Code: code, Message: normalizedMessage}, nil
}

// InferenceIngestionProcessingJob holds per-model processing state for an ingestion candidate.
type InferenceIngestionProcessingJob struct {
	ID                string
	ProcessingRunID   *string `db:"processing_run_id"`
	CandidateID       string  `db:"candidate_id"`
	TenantID          string  `db:"tenant_id"`
	ModelName         string  `db:"model_name"`
	ModelVersion      *string `db:"model_version"`
	Modality          *string `db:"modality"`
	Status            InferenceIngestionProcessingJobStatus
	StudyServiceJobID *string                                        `db:"study_service_job_id"`
	ErrorMessage      *string                                        `db:"error_message"`
	SkipReasonCode    *InferenceIngestionProcessingJobSkipReasonCode `db:"skip_reason_code"`
	SkipReasonMessage *string                                        `db:"skip_reason_message"`
	LastEventID       *string                                        `db:"last_event_id"`
	LastEventSequence *int64                                         `db:"last_event_sequence"`
	StartedAt         *time.Time                                     `db:"started_at"`
	CompletedAt       *time.Time                                     `db:"completed_at"`
	CreatedAt         time.Time                                      `db:"created_at"`
	UpdatedAt         time.Time                                      `db:"updated_at"`
}

// GetSkipReason returns the job's structured skip reason when a valid code is present.
func (entity *InferenceIngestionProcessingJob) GetSkipReason() *InferenceIngestionProcessingJobSkipReason {
	if entity.SkipReasonCode == nil || !entity.SkipReasonCode.IsValid() {
		return nil
	}
	return &InferenceIngestionProcessingJobSkipReason{
		Code:    *entity.SkipReasonCode,
		Message: entity.SkipReasonMessage,
	}
}

// GetModelName returns the model name of inference ingestion processing job entity that can be used for naming schemas.
func (entity *InferenceIngestionProcessingJob) GetModelName() string {
	return "ingestion_processing_jobs"
}
