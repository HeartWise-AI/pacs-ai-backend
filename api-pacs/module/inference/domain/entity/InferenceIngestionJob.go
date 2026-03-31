package entity

import (
	"time"
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
	Modalities             []string
	IntervalInMinutes      uint                        `db:"interval_in_minutes"`
	ScheduleStartTimestamp time.Time                   `db:"schedule_start_timestamp"`
	ScheduleEndTimestamp   time.Time                   `db:"schedule_end_timestamp"`
	Status                 InferenceIngestionJobStatus // enum
	CreatedAt              time.Time                   `db:"created_at"`
	UpdatedAt              time.Time                   `db:"updated_at"`
}

// GetModelName returns the model name of inference ingestion jobs entity that can be used for naming schemas
func (entity *InferenceIngestionJob) GetModelName() string {
	return "inference_ingestion_jobs"
}
