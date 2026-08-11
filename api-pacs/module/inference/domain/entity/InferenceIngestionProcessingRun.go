package entity

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

type InferenceIngestionProcessingRunTrigger string
type InferenceIngestionProcessingRunPhase string
type InferenceIngestionProcessingRunOutcome string

const (
	InferenceIngestionProcessingRunTriggerAuto            InferenceIngestionProcessingRunTrigger = "AUTO"
	InferenceIngestionProcessingRunTriggerManualReprocess InferenceIngestionProcessingRunTrigger = "MANUAL_REPROCESS"
	InferenceIngestionProcessingRunTriggerLegacyImport    InferenceIngestionProcessingRunTrigger = "LEGACY_IMPORT"
)

const (
	InferenceIngestionProcessingRunPhaseQueued     InferenceIngestionProcessingRunPhase = "QUEUED"
	InferenceIngestionProcessingRunPhaseProcessing InferenceIngestionProcessingRunPhase = "PROCESSING"
	InferenceIngestionProcessingRunPhaseTerminal   InferenceIngestionProcessingRunPhase = "TERMINAL"
)

const (
	InferenceIngestionProcessingRunOutcomeSuccess          InferenceIngestionProcessingRunOutcome = "SUCCESS"
	InferenceIngestionProcessingRunOutcomeSuccessWithSkips InferenceIngestionProcessingRunOutcome = "SUCCESS_WITH_SKIPS"
	InferenceIngestionProcessingRunOutcomePartialSuccess   InferenceIngestionProcessingRunOutcome = "PARTIAL_SUCCESS"
	InferenceIngestionProcessingRunOutcomeNoResult         InferenceIngestionProcessingRunOutcome = "NO_RESULT"
	InferenceIngestionProcessingRunOutcomeFailed           InferenceIngestionProcessingRunOutcome = "FAILED"
	InferenceIngestionProcessingRunOutcomeCancelled        InferenceIngestionProcessingRunOutcome = "CANCELLED"
)

// InferenceIngestionProcessingRunAttentionReason describes why a run needs operator attention.
type InferenceIngestionProcessingRunAttentionReason struct {
	Code    string  `json:"code"`
	Message *string `json:"message,omitempty"`
}

// InferenceIngestionProcessingRunAttentionReasons maps the run attention JSONB column.
type InferenceIngestionProcessingRunAttentionReasons []InferenceIngestionProcessingRunAttentionReason

// Scan implements sql.Scanner for PostgreSQL JSONB values.
func (reasons *InferenceIngestionProcessingRunAttentionReasons) Scan(value interface{}) error {
	if value == nil {
		*reasons = InferenceIngestionProcessingRunAttentionReasons{}
		return nil
	}

	var data []byte
	switch typed := value.(type) {
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	default:
		return fmt.Errorf("unsupported attention reasons value type %T", value)
	}

	if len(data) == 0 {
		*reasons = InferenceIngestionProcessingRunAttentionReasons{}
		return nil
	}

	return json.Unmarshal(data, reasons)
}

// Value implements driver.Valuer for PostgreSQL JSONB values.
func (reasons InferenceIngestionProcessingRunAttentionReasons) Value() (driver.Value, error) {
	if reasons == nil {
		reasons = InferenceIngestionProcessingRunAttentionReasons{}
	}
	return json.Marshal(reasons)
}

// InferenceIngestionProcessingRun represents one distinct study-processing attempt.
type InferenceIngestionProcessingRun struct {
	ID                         string
	TenantID                   string                                          `db:"tenant_id"`
	StudyInstanceUID           string                                          `db:"study_instance_uid"`
	RunNumber                  int                                             `db:"run_number"`
	RunTrigger                 InferenceIngestionProcessingRunTrigger          `db:"run_trigger"`
	RequestedByUserID          *string                                         `db:"requested_by_user_id"`
	Phase                      InferenceIngestionProcessingRunPhase            `db:"phase"`
	Outcome                    *InferenceIngestionProcessingRunOutcome         `db:"outcome"`
	AttentionRequired          bool                                            `db:"attention_required"`
	AttentionReasons           InferenceIngestionProcessingRunAttentionReasons `db:"attention_reasons"`
	Version                    int64                                           `db:"version"`
	ReconciliationFailureCount int                                             `db:"reconciliation_failure_count"`
	LastReconciliationAt       *time.Time                                      `db:"last_reconciliation_at"`
	StartedAt                  *time.Time                                      `db:"started_at"`
	CompletedAt                *time.Time                                      `db:"completed_at"`
	CreatedAt                  time.Time                                       `db:"created_at"`
	UpdatedAt                  time.Time                                       `db:"updated_at"`
}

// GetModelName returns the PostgreSQL table name for processing runs.
func (entity *InferenceIngestionProcessingRun) GetModelName() string {
	return "ingestion_processing_runs"
}
