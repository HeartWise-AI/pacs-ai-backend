package entity

import (
	"time"
)

type InferenceIngestionProcessingJobStatus string

const (
	InferenceIngestionProcessingJobStatusQueued    InferenceIngestionProcessingJobStatus = "queued"
	InferenceIngestionProcessingJobStatusRunning   InferenceIngestionProcessingJobStatus = "running"
	InferenceIngestionProcessingJobStatusCompleted InferenceIngestionProcessingJobStatus = "completed"
	InferenceIngestionProcessingJobStatusFailed    InferenceIngestionProcessingJobStatus = "failed"
)

// InferenceIngestionProcessingJob holds per-model processing state for an ingestion candidate.
type InferenceIngestionProcessingJob struct {
	ID                string
	CandidateID       string                               `db:"candidate_id"`
	TenantID          string                               `db:"tenant_id"`
	ModelName         string                               `db:"model_name"`
	ModelVersion      *string                              `db:"model_version"`
	Modality          *string                              `db:"modality"`
	Status            InferenceIngestionProcessingJobStatus
	StudyServiceJobID *string                              `db:"study_service_job_id"`
	ErrorMessage      *string                              `db:"error_message"`
	StartedAt         *time.Time                           `db:"started_at"`
	CompletedAt       *time.Time                           `db:"completed_at"`
	CreatedAt         time.Time                            `db:"created_at"`
	UpdatedAt         time.Time                            `db:"updated_at"`
}

// GetModelName returns the model name of inference ingestion processing job entity that can be used for naming schemas.
func (entity *InferenceIngestionProcessingJob) GetModelName() string {
	return "ingestion_processing_jobs"
}
