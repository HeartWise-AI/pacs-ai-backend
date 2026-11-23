package types

import (
	"encoding/json"
	"time"

	"api-pacs/module/inference/domain/entity"
)

// GetWorklistStudyStatuses scopes the current study-status snapshot to the
// authenticated tenant. StudyInstanceUIDs is optional and is intended for the
// studies visible on the current frontend page.
type GetWorklistStudyStatuses struct {
	TenantID          string
	StudyInstanceUIDs []string
	Limit             int
	Offset            int
}

// GetStudyProcessingRunHistory scopes paginated run history to one tenant and
// one logical study.
type GetStudyProcessingRunHistory struct {
	TenantID         string
	StudyInstanceUID string
	Limit            int
	Offset           int
}

// GetProcessingRunDetail scopes one run-detail lookup to the authenticated
// tenant. Run IDs alone are never treated as authorization.
type GetProcessingRunDetail struct {
	TenantID string
	RunID    string
}

// GetProcessingRunExecutionResult scopes one lazy result lookup to the
// authenticated tenant, parent run, and stable model execution identifier.
type GetProcessingRunExecutionResult struct {
	TenantID    string
	RunID       string
	ExecutionID string
	// AllowModel, when set, must accept the result's model-info modelId
	// (custom-role model access); otherwise the call is ForbiddenAccess.
	AllowModel func(modelID string) bool
}

// WorklistPage describes bounded list results without requiring a separate
// total-count query.
type WorklistPage struct {
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"hasMore"`
}

// ProcessingRunCounts exposes aggregate model outcomes without inference
// result payloads.
type ProcessingRunCounts struct {
	Expected  int `json:"expectedModels"`
	Pending   int `json:"pendingModels"`
	Queued    int `json:"queuedModels"`
	Running   int `json:"runningModels"`
	Completed int `json:"completedModels"`
	Failed    int `json:"failedModels"`
	Skipped   int `json:"skippedModels"`
	Cancelled int `json:"cancelledModels"`
	Active    int `json:"activeModels"`
}

// WorklistStudyStatus is the current flattened status for one logical
// tenant/study. Run fields are nullable because retrieval can exist before the
// first processing run is created.
type WorklistStudyStatus struct {
	StudyInstanceUID  string                                                 `json:"studyInstanceUID"`
	IngestionStatus   entity.InferenceIngestionCandidateStatus               `json:"ingestionStatus"`
	RetrievalState    *string                                                `json:"retrievalState"`
	RetrievalError    *string                                                `json:"retrievalError"`
	RunID             *string                                                `json:"runId"`
	RunNumber         *int                                                   `json:"runNumber"`
	Trigger           *entity.InferenceIngestionProcessingRunTrigger         `json:"trigger"`
	Phase             *entity.InferenceIngestionProcessingRunPhase           `json:"phase"`
	Outcome           *entity.InferenceIngestionProcessingRunOutcome         `json:"outcome"`
	AttentionRequired bool                                                   `json:"attentionRequired"`
	AttentionReasons  entity.InferenceIngestionProcessingRunAttentionReasons `json:"attentionReasons"`
	ProcessingRunCounts
	Version     *int64     `json:"version"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// WorklistStudyStatusPage is the authoritative REST snapshot used for initial
// load and SSE reconnect recovery.
type WorklistStudyStatusPage struct {
	Studies []WorklistStudyStatus `json:"studies"`
	WorklistPage
}

// ProcessingRunSummary contains the persisted run aggregate and its model
// counts. It is shared by history and detail responses.
type ProcessingRunSummary struct {
	RunID             string                                                 `json:"runId"`
	StudyInstanceUID  string                                                 `json:"studyInstanceUID"`
	RunNumber         int                                                    `json:"runNumber"`
	Trigger           entity.InferenceIngestionProcessingRunTrigger          `json:"trigger"`
	Phase             entity.InferenceIngestionProcessingRunPhase            `json:"phase"`
	Outcome           *entity.InferenceIngestionProcessingRunOutcome         `json:"outcome"`
	AttentionRequired bool                                                   `json:"attentionRequired"`
	AttentionReasons  entity.InferenceIngestionProcessingRunAttentionReasons `json:"attentionReasons"`
	ProcessingRunCounts
	Version     int64      `json:"version"`
	StartedAt   *time.Time `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// ProcessingRunExecutionSummary exposes model state and operator-facing
// failure/skip context. Detailed Python inference results are intentionally not
// part of this contract.
type ProcessingRunExecutionSummary struct {
	ExecutionID  string                                            `json:"executionId"`
	ModelName    string                                            `json:"modelName"`
	ModelVersion *string                                           `json:"modelVersion"`
	Modality     *string                                           `json:"modality"`
	Status       entity.InferenceIngestionProcessingJobStatus      `json:"status"`
	ErrorMessage *string                                           `json:"errorMessage"`
	SkipReason   *entity.InferenceIngestionProcessingJobSkipReason `json:"skipReason"`
	StartedAt    *time.Time                                        `json:"startedAt"`
	CompletedAt  *time.Time                                        `json:"completedAt"`
	UpdatedAt    time.Time                                         `json:"updatedAt"`
}

// ProcessingRunWaitingReason tells a client why an active run is not computing.
type ProcessingRunWaitingReason string

const (
	// ProcessingRunWaitingReasonStudyGrowing: the remaining models wait for
	// the study to stop receiving images (the exam is still in progress).
	ProcessingRunWaitingReasonStudyGrowing ProcessingRunWaitingReason = "study_growing"
	// ProcessingRunWaitingReasonStale: the remaining models were never started
	// and the reconciler flagged the run (PENDING_STALE, EXPECTED_JOB_MISSING).
	ProcessingRunWaitingReasonStale ProcessingRunWaitingReason = "stale"
)

// ProcessingRunDetail contains one run aggregate and its frozen model plan.
type ProcessingRunDetail struct {
	ProcessingRunSummary
	Executions []ProcessingRunExecutionSummary `json:"executions"`
	// Cancellable is true while the run is active and has at least one model
	// that has not started computing.
	Cancellable bool `json:"cancellable"`
	// CancelledAt is when a user cancelled the run's waiting models.
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`
	// WaitingReason is absent while the run is computing, or when it is merely
	// queued. "study_growing" is reported by the run-detail endpoint only.
	WaitingReason ProcessingRunWaitingReason `json:"waitingReason,omitempty"`
	// StableAfter is the earliest time the study is considered stable if no
	// further image arrives; set only with WaitingReason "study_growing".
	StableAfter *time.Time `json:"stableAfter,omitempty"`
}

// CancelProcessingRun scopes a user cancellation to the authenticated tenant.
type CancelProcessingRun struct {
	TenantID string
	RunID    string
	UserID   string
}

// ProcessingRunExecutionResult is the stable public envelope for one completed
// execution. Result remains opaque JSON so the transport is model-agnostic.
type ProcessingRunExecutionResult struct {
	RunID            string                                       `json:"runId"`
	ExecutionID      string                                       `json:"executionId"`
	StudyInstanceUID string                                       `json:"studyInstanceUID"`
	ModelName        string                                       `json:"modelName"`
	ModelVersion     *string                                      `json:"modelVersion"`
	Status           entity.InferenceIngestionProcessingJobStatus `json:"status"`
	CompletedAt      time.Time                                    `json:"completedAt"`
	Result           json.RawMessage                              `json:"result"`
	// SelectedViews is null when the run did not record its view selection.
	SelectedViews json.RawMessage `json:"selectedViews"`
}

// StudyProcessingRunHistoryPage returns complete runs newest first.
type StudyProcessingRunHistoryPage struct {
	Runs []ProcessingRunDetail `json:"runs"`
	WorklistPage
}
