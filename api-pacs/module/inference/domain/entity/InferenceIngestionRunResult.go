package entity

import (
	"time"
)

type IngestionRunStatus string

const (
	IngestionRunStatusSuccess IngestionRunStatus = "SUCCESS"
	IngestsionRunStatusFailed IngestionRunStatus = "FAILED"
)

// InferenceIngestionRunResult holds the inference ingestion run result entity fields
type InferenceIngestionRunResult struct {
	ID               string
	JobID            string                  `db:"job_id"`
	StudyInstanceUID string                  `db:"study_instance_uid"`
	InferenceOutput  *map[string]interface{} `db:"inference_output"`
	ErrorMessage     *string                 `db:"error_message"`
	Status           IngestionRunStatus      // enum
	CreatedAt        time.Time               `db:"created_at"`
	UpdatedAt        time.Time               `db:"updated_at"`
}

// GetModelName returns the model name of inference ingestion run result entity that can be used for naming schemas
func (entity *InferenceIngestionRunResult) GetModelName() string {
	return "inference_ingestion_run_results"
}
