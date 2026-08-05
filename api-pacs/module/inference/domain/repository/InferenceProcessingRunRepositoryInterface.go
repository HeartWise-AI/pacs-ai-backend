package repository

import (
	"context"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceProcessingRunRepositoryInterface owns transactional processing-run persistence.
type InferenceProcessingRunRepositoryInterface interface {
	// ApplyProcessingRunExecutionTransition atomically applies one execution event and recalculates its run.
	ApplyProcessingRunExecutionTransition(ctx context.Context, data types.ApplyInferenceIngestionProcessingTransition) (types.ApplyInferenceIngestionProcessingTransitionResult, error)
	// CreateProcessingRun atomically allocates the next study-local run number and inserts the run.
	CreateProcessingRun(ctx context.Context, data types.CreateInferenceIngestionProcessingRun) (entity.InferenceIngestionProcessingRun, error)
	// CreateProcessingRunPlan atomically freezes a run and its expected executions.
	// Automatic requests reuse the active plan; manual requests conflict while a run is active.
	CreateProcessingRunPlan(ctx context.Context, data types.CreateInferenceIngestionProcessingRunPlan) (types.CreateInferenceIngestionProcessingRunPlanResult, error)
	// SelectActiveProcessingRun returns the tenant-scoped non-terminal run for a study.
	SelectActiveProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error)
	// SelectProcessingRun returns one tenant-scoped processing run by ID.
	SelectProcessingRun(ctx context.Context, tenantID, processingRunID string) (entity.InferenceIngestionProcessingRun, error)
	// SelectLatestProcessingRun returns the newest tenant-scoped run for a study.
	SelectLatestProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error)
	// ListProcessingRunHistory returns tenant-scoped runs ordered newest first.
	ListProcessingRunHistory(ctx context.Context, data types.ListInferenceIngestionProcessingRuns) ([]entity.InferenceIngestionProcessingRun, error)
	// ListProcessingRunsForReconciliation returns bounded active stale or attention-required work.
	// Terminal history is never continuously polled.
	ListProcessingRunsForReconciliation(ctx context.Context, data types.ListInferenceIngestionProcessingRunsForReconciliation) ([]entity.InferenceIngestionProcessingRun, error)
	// RecordProcessingRunReconciliationAttempt atomically increments failures or resets them after success.
	RecordProcessingRunReconciliationAttempt(ctx context.Context, data types.RecordInferenceIngestionProcessingRunReconciliationAttempt) (entity.InferenceIngestionProcessingRun, error)
	// ListProcessingRunExecutions returns the expected model executions for a tenant-scoped run.
	ListProcessingRunExecutions(ctx context.Context, tenantID, processingRunID string) ([]entity.InferenceIngestionProcessingJob, error)
	// SelectProcessingRunExecution returns one exact tenant/run/candidate/model execution.
	SelectProcessingRunExecution(ctx context.Context, tenantID, processingRunID, candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error)
	// UpdateProcessingRunAggregate applies an optimistic versioned aggregate update.
	UpdateProcessingRunAggregate(ctx context.Context, data types.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error)
}
