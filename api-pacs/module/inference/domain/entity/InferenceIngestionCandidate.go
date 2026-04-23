package entity

import (
	"time"

	"github.com/lib/pq"
)

type InferenceIngestionCandidateStatus string

const (
	InferenceIngestionCandidateStatusDiscovered      InferenceIngestionCandidateStatus = "DISCOVERED"
	InferenceIngestionCandidateStatusGrowing         InferenceIngestionCandidateStatus = "GROWING"
	InferenceIngestionCandidateStatusStable          InferenceIngestionCandidateStatus = "STABLE"
	InferenceIngestionCandidateStatusRetrievalQueued InferenceIngestionCandidateStatus = "RETRIEVAL_QUEUED"
	InferenceIngestionCandidateStatusRetrieved       InferenceIngestionCandidateStatus = "RETRIEVED"
	InferenceIngestionCandidateStatusDisappeared     InferenceIngestionCandidateStatus = "DISAPPEARED"
	InferenceIngestionCandidateStatusFailed          InferenceIngestionCandidateStatus = "FAILED"
)

// InferenceIngestionCandidate holds the ingestion candidate entity fields
type InferenceIngestionCandidate struct {
	ID                        string
	TenantID                  string `db:"tenant_id"`
	IngestionJobID            string `db:"ingestion_job_id"`
	StudyInstanceUID          string `db:"study_instance_uid"`
	StudyDate                 *string `db:"study_date"`
	StudyTime                 *string `db:"study_time"`
	ModalitiesInStudy         *string `db:"modalities_in_study"`
	PatientID                 *string `db:"patient_id"`
	AccessionNumber           *string `db:"accession_number"`
	SeriesCount               *int `db:"series_count"`
	InstanceCount             *int `db:"instance_count"`
	FirstSeenAt               time.Time `db:"first_seen_at"`
	LastSeenAt                time.Time `db:"last_seen_at"`
	LastChangedAt             time.Time `db:"last_changed_at"`
	MissingPolls              int `db:"missing_polls"`
	Status                    InferenceIngestionCandidateStatus // enum
	RetrievalQueuedAt         *time.Time `db:"retrieval_queued_at"`
	RetrievedAt               *time.Time `db:"retrieved_at"`
	OrthancJobIDs             pq.StringArray `db:"orthanc_job_ids"`
	LastRetrievalState        *string `db:"last_retrieval_state"`
	LastRetrievalError        *string `db:"last_retrieval_error"`
	LastRetrievalErrorDetails *string `db:"last_retrieval_error_details"`
	LastRetrievalCheckedAt    *time.Time `db:"last_retrieval_checked_at"`
	CreatedAt                 time.Time `db:"created_at"`
	UpdatedAt                 time.Time `db:"updated_at"`
}

// GetModelName returns the model name of inference ingestion candidate entity that can be used for naming schemas
func (entity *InferenceIngestionCandidate) GetModelName() string {
	return "inference_ingestion_candidates"
}
