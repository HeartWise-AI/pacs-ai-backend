package entity

import (
	"time"

	"github.com/lib/pq"
)

type InferenceIngestionJobStatus string

const (
	InferenceIngestionJobStatusRunning InferenceIngestionJobStatus = "RUNNING"
	InferenceIngestionJobStatusStopped InferenceIngestionJobStatus = "STOPPED"
)

// InferenceModel holds the inference ingestion entity fields
type InferenceIngestionJob struct {
	ID                     string
	TenantID               string `db:"tenant_id"`
	DICOMModality          string `db:"dicom_modality"`
	ContainerID            string `db:"container_id"`
	ModelID                string `db:"model_id"`
	ModelName              string `db:"model_name"`
	ModelVersion           string `db:"model_version"`
	Modalities             pq.StringArray
	StabilityMinutes       uint                        `db:"stability_minutes"`
	RecentWindowMinutes    uint                        `db:"recent_window_minutes"`
	MissingPollsThreshold  uint                        `db:"missing_polls_threshold"`
	StudyTimeStart         *string                     `db:"study_time_start"`
	StudyTimeEnd           *string                     `db:"study_time_end"`
	ScheduleStartTimestamp time.Time                   `db:"schedule_start_timestamp"`
	ScheduleEndTimestamp   time.Time                   `db:"schedule_end_timestamp"`
	Status                 InferenceIngestionJobStatus // enum
	LastExecutedAt         *time.Time                  `db:"last_executed_at"`
	CreatedAt              time.Time                   `db:"created_at"`
	UpdatedAt              time.Time                   `db:"updated_at"`
	// CreatedByUserID is the workspace user who created the job; nil for jobs
	// created before it was recorded and for internal viewer-request jobs.
	CreatedByUserID *string `db:"created_by_user_id"`
	// NotificationsEnabled decides whether runs started by this job notify
	// anyone (the creator and workspace admins). Status events are unaffected.
	NotificationsEnabled bool `db:"notifications_enabled"`
}

// InferenceIngestionJobColumns lists the ingestion_jobs columns the gateway
// reads. Selecting them explicitly (rather than *) keeps a running gateway
// working when a later additive migration adds a column it does not know.
const InferenceIngestionJobColumns = "id, tenant_id, dicom_modality, container_id, model_id, model_name, model_version, " +
	"modalities, stability_minutes, recent_window_minutes, missing_polls_threshold, study_time_start, study_time_end, " +
	"schedule_start_timestamp, schedule_end_timestamp, status, last_executed_at, created_at, updated_at, " +
	"created_by_user_id, notifications_enabled"

// GetModelName returns the model name of inference ingestion jobs entity that can be used for naming schemas
func (entity *InferenceIngestionJob) GetModelName() string {
	return "ingestion_jobs"
}
