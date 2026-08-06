package repository

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceProcessingRunRepository persists processing runs and their executions.
type InferenceProcessingRunRepository struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

const (
	processingTransitionOutcomeApplied  = "applied"
	processingTransitionOutcomeIgnored  = "ignored"
	processingTransitionOutcomeReplayed = "replayed"
)

func processingRunError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New(apiError.MissingRecord)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return errors.New(apiError.DuplicateRecord)
	}
	log.Println(err)
	return errors.New(apiError.DatabaseError)
}

// ApplyProcessingRunExecutionTransition serializes one execution event with its aggregate update.
func (repository *InferenceProcessingRunRepository) ApplyProcessingRunExecutionTransition(
	ctx context.Context,
	data types.ApplyInferenceIngestionProcessingTransition,
) (types.ApplyInferenceIngestionProcessingTransitionResult, error) {
	tx, err := repository.PostgresSQLDBHandlerInterface.Begin()
	if err != nil {
		return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
	}
	defer tx.Rollback()

	var run entity.InferenceIngestionProcessingRun
	if err = tx.GetContext(ctx, &run, `
		SELECT * FROM ingestion_processing_runs
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, data.ProcessingRunID, data.TenantID); err != nil {
		return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
	}

	var execution entity.InferenceIngestionProcessingJob
	if err = tx.GetContext(ctx, &execution, `
		SELECT * FROM ingestion_processing_jobs
		WHERE id = $1 AND processing_run_id = $2 AND candidate_id = $3
		  AND tenant_id = $4 AND model_name = $5
		FOR UPDATE
	`, data.ExecutionID, data.ProcessingRunID, data.CandidateID, data.TenantID, data.ModelName); err != nil {
		return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
	}

	if data.EventID != nil && execution.LastEventID != nil && *data.EventID == *execution.LastEventID {
		return types.ApplyInferenceIngestionProcessingTransitionResult{
			Outcome: processingTransitionOutcomeReplayed, Execution: execution, Run: run,
		}, nil
	}
	if data.EventSequence != nil && execution.LastEventSequence != nil && *data.EventSequence <= *execution.LastEventSequence {
		return types.ApplyInferenceIngestionProcessingTransitionResult{
			Outcome: processingTransitionOutcomeIgnored, Execution: execution, Run: run,
		}, nil
	}
	if !execution.Status.CanTransitionTo(data.Status) {
		return types.ApplyInferenceIngestionProcessingTransitionResult{
			Outcome: processingTransitionOutcomeIgnored, Execution: execution, Run: run,
		}, nil
	}

	legacyReplay := data.EventID == nil && data.EventSequence == nil && execution.Status == data.Status
	if !legacyReplay {
		var skipReasonCode interface{}
		var skipReasonMessage interface{}
		if data.SkipReason != nil {
			skipReasonCode = data.SkipReason.Code
			if data.SkipReason.Message != nil {
				skipReasonMessage = *data.SkipReason.Message
			}
		}

		if err = tx.GetContext(ctx, &execution, `
			UPDATE ingestion_processing_jobs SET
				status = $1,
				model_version = COALESCE($2, model_version),
				modality = COALESCE($3, modality),
				study_service_job_id = COALESCE($4, study_service_job_id),
				error_message = $5,
				skip_reason_code = $6,
				skip_reason_message = $7,
				last_event_id = COALESCE($8, last_event_id),
				last_event_sequence = COALESCE($9, last_event_sequence),
				started_at = COALESCE($10, started_at),
				completed_at = COALESCE($11, completed_at)
			WHERE id = $12
			RETURNING *
		`, data.Status, data.ModelVersion, data.Modality, data.StudyServiceJobID,
			data.ErrorMessage, skipReasonCode, skipReasonMessage, data.EventID,
			data.EventSequence, data.StartedAt, data.CompletedAt, data.ExecutionID); err != nil {
			return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
		}
	}

	executions := make([]entity.InferenceIngestionProcessingJob, 0)
	if err = tx.SelectContext(ctx, &executions, `
		SELECT * FROM ingestion_processing_jobs
		WHERE processing_run_id = $1
		ORDER BY created_at ASC, id ASC
	`, data.ProcessingRunID); err != nil {
		return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
	}

	aggregate := entity.AggregateInferenceIngestionProcessingRun(
		entity.InferenceIngestionProcessingRunAggregationInput{Run: run, Executions: executions},
	)
	if err = tx.GetContext(ctx, &run, `
		UPDATE ingestion_processing_runs SET
			phase = $1, outcome = $2,
			attention_required = $3, attention_reasons = $4,
			started_at = $5, completed_at = $6, version = version + 1
		WHERE id = $7 AND tenant_id = $8
		RETURNING *
	`, aggregate.Phase, aggregate.Outcome, aggregate.AttentionRequired,
		aggregate.AttentionReasons, aggregate.StartedAt, aggregate.CompletedAt,
		data.ProcessingRunID, data.TenantID); err != nil {
		return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
	}

	if err = tx.Commit(); err != nil {
		return types.ApplyInferenceIngestionProcessingTransitionResult{}, processingRunError(err)
	}
	outcome := processingTransitionOutcomeApplied
	if legacyReplay {
		outcome = processingTransitionOutcomeReplayed
	}
	return types.ApplyInferenceIngestionProcessingTransitionResult{
		Outcome: outcome, Changed: true, Execution: execution, Run: run, Counts: aggregate.Counts,
	}, nil
}

// CreateProcessingRun atomically allocates the next study-local run number and inserts the run.
func (repository *InferenceProcessingRunRepository) CreateProcessingRun(ctx context.Context, data types.CreateInferenceIngestionProcessingRun) (entity.InferenceIngestionProcessingRun, error) {
	tx, err := repository.PostgresSQLDBHandlerInterface.Begin()
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}
	defer tx.Rollback()

	lockKey := data.TenantID + "\x00" + data.StudyInstanceUID
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}

	var runNumber int
	err = tx.GetContext(ctx, &runNumber, `
		SELECT COALESCE(MAX(run_number), 0) + 1
		FROM ingestion_processing_runs
		WHERE tenant_id = $1 AND study_instance_uid = $2
	`, data.TenantID, data.StudyInstanceUID)
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}

	var run entity.InferenceIngestionProcessingRun
	err = tx.GetContext(ctx, &run, `
		INSERT INTO ingestion_processing_runs (
			id, tenant_id, study_instance_uid, run_number, run_trigger, phase
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING *
	`, data.ID, data.TenantID, data.StudyInstanceUID, runNumber, data.RunTrigger, data.Phase)
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}

	if err = tx.Commit(); err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}
	return run, nil
}

// CreateProcessingRunPlan atomically freezes a run and its expected executions.
// An automatic request reuses the active plan. A manual request conflicts until the active run terminates.
func (repository *InferenceProcessingRunRepository) CreateProcessingRunPlan(ctx context.Context, data types.CreateInferenceIngestionProcessingRunPlan) (types.CreateInferenceIngestionProcessingRunPlanResult, error) {
	tx, err := repository.PostgresSQLDBHandlerInterface.Begin()
	if err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}
	defer tx.Rollback()

	lockKey := data.Run.TenantID + "\x00" + data.Run.StudyInstanceUID
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	var activeRun entity.InferenceIngestionProcessingRun
	err = tx.GetContext(ctx, &activeRun, `
		SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = $1 AND study_instance_uid = $2 AND phase <> 'TERMINAL'
		ORDER BY run_number DESC LIMIT 1
	`, data.Run.TenantID, data.Run.StudyInstanceUID)
	if err == nil {
		if data.Run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerAuto {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, errors.New(apiError.DuplicateRecord)
		}

		executions := make([]entity.InferenceIngestionProcessingJob, 0)
		if err = tx.SelectContext(ctx, &executions, `
			SELECT * FROM ingestion_processing_jobs
			WHERE processing_run_id = $1
			ORDER BY created_at ASC, id ASC
		`, activeRun.ID); err != nil {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
		}

		if err = tx.Commit(); err != nil {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
		}
		return types.CreateInferenceIngestionProcessingRunPlanResult{
			Run: activeRun, Executions: executions, Created: false,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	var runNumber int
	if err = tx.GetContext(ctx, &runNumber, `
		SELECT COALESCE(MAX(run_number), 0) + 1
		FROM ingestion_processing_runs
		WHERE tenant_id = $1 AND study_instance_uid = $2
	`, data.Run.TenantID, data.Run.StudyInstanceUID); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	var run entity.InferenceIngestionProcessingRun
	if err = tx.GetContext(ctx, &run, `
		INSERT INTO ingestion_processing_runs (
			id, tenant_id, study_instance_uid, run_number, run_trigger, phase
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING *
	`, data.Run.ID, data.Run.TenantID, data.Run.StudyInstanceUID, runNumber, data.Run.RunTrigger, data.Run.Phase); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	executions := make([]entity.InferenceIngestionProcessingJob, 0, len(data.Executions))
	for _, expected := range data.Executions {
		var execution entity.InferenceIngestionProcessingJob
		if err = tx.GetContext(ctx, &execution, `
			INSERT INTO ingestion_processing_jobs (
				id, processing_run_id, candidate_id, tenant_id, model_name,
				model_version, modality, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING *
		`, expected.ID, run.ID, expected.CandidateID, run.TenantID, expected.ModelName,
			expected.ModelVersion, expected.Modality, entity.InferenceIngestionProcessingJobStatusPending); err != nil {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
		}
		executions = append(executions, execution)
	}

	if err = tx.Commit(); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}
	return types.CreateInferenceIngestionProcessingRunPlanResult{
		Run: run, Executions: executions, Created: true,
	}, nil
}

func (repository *InferenceProcessingRunRepository) selectStudyRun(ctx context.Context, tenantID, studyInstanceUID, suffix string) (entity.InferenceIngestionProcessingRun, error) {
	var run entity.InferenceIngestionProcessingRun
	query := `SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = :tenant_id AND study_instance_uid = :study_instance_uid ` + suffix
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(query, map[string]interface{}{
		"tenant_id": tenantID, "study_instance_uid": studyInstanceUID,
	}, &run)
	return run, processingRunError(err)
}

// SelectActiveProcessingRun returns the tenant-scoped non-terminal run for a study.
func (repository *InferenceProcessingRunRepository) SelectActiveProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	return repository.selectStudyRun(ctx, tenantID, studyInstanceUID, "AND phase <> 'TERMINAL' ORDER BY run_number DESC LIMIT 1")
}

// SelectProcessingRun returns one tenant-scoped processing run by ID.
func (repository *InferenceProcessingRunRepository) SelectProcessingRun(ctx context.Context, tenantID, processingRunID string) (entity.InferenceIngestionProcessingRun, error) {
	var run entity.InferenceIngestionProcessingRun
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(`
		SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = :tenant_id AND id = :processing_run_id
	`, map[string]interface{}{
		"tenant_id": tenantID, "processing_run_id": processingRunID,
	}, &run)
	return run, processingRunError(err)
}

// SelectLatestProcessingRun returns the newest tenant-scoped run for a study.
func (repository *InferenceProcessingRunRepository) SelectLatestProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	return repository.selectStudyRun(ctx, tenantID, studyInstanceUID, "ORDER BY run_number DESC LIMIT 1")
}

// ListProcessingRunHistory returns tenant-scoped runs ordered newest first.
func (repository *InferenceProcessingRunRepository) ListProcessingRunHistory(ctx context.Context, data types.ListInferenceIngestionProcessingRuns) ([]entity.InferenceIngestionProcessingRun, error) {
	runs := make([]entity.InferenceIngestionProcessingRun, 0)
	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = :tenant_id AND study_instance_uid = :study_instance_uid
		ORDER BY run_number DESC LIMIT :limit OFFSET :offset
	`, data, &runs)
	return runs, processingRunError(err)
}

// ListProcessingRunHistoryPage returns bounded tenant-scoped runs newest first.
// One extra row is requested to derive HasMore without a count query.
func (repository *InferenceProcessingRunRepository) ListProcessingRunHistoryPage(ctx context.Context, data types.ListInferenceIngestionProcessingRuns) (types.InferenceIngestionProcessingRunHistoryPage, error) {
	query := data
	query.Limit++

	runs, err := repository.ListProcessingRunHistory(ctx, query)
	if err != nil {
		return types.InferenceIngestionProcessingRunHistoryPage{}, err
	}

	hasMore := len(runs) > data.Limit
	if hasMore {
		runs = runs[:data.Limit]
	}

	return types.InferenceIngestionProcessingRunHistoryPage{Runs: runs, HasMore: hasMore}, nil
}

// ListProcessingRunsForReconciliation returns a bounded active worker batch.
// Terminal runs remain queryable by the worklist APIs but are never selected
// for continuous reconciliation. The repository performs a coarse
// earliest-threshold filter; the service applies the exact pending, queued,
// running, and model-specific thresholds.
func (repository *InferenceProcessingRunRepository) ListProcessingRunsForReconciliation(
	ctx context.Context,
	data types.ListInferenceIngestionProcessingRunsForReconciliation,
) ([]entity.InferenceIngestionProcessingRun, error) {
	runs := make([]entity.InferenceIngestionProcessingRun, 0)
	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT runs.* FROM ingestion_processing_runs runs
		WHERE runs.phase <> 'TERMINAL'
		  AND (
			runs.attention_required = TRUE
			OR (
				runs.updated_at <= :active_stale_before
				OR EXISTS (
					SELECT 1 FROM ingestion_processing_jobs jobs
					WHERE jobs.processing_run_id = runs.id
					  AND jobs.status IN ('pending', 'queued', 'running')
					  AND jobs.updated_at <= :active_stale_before
				)
			  )
		   )
		ORDER BY runs.attention_required DESC, runs.updated_at ASC, runs.id ASC
		LIMIT :limit
	`, data, &runs)
	return runs, processingRunError(err)
}

// RecordProcessingRunReconciliationAttempt keeps failure tracking durable
// across worker and service restarts. The increment/reset is one atomic update.
func (repository *InferenceProcessingRunRepository) RecordProcessingRunReconciliationAttempt(
	ctx context.Context,
	data types.RecordInferenceIngestionProcessingRunReconciliationAttempt,
) (entity.InferenceIngestionProcessingRun, error) {
	var run entity.InferenceIngestionProcessingRun
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(`
		UPDATE ingestion_processing_runs SET
			reconciliation_failure_count = CASE
				WHEN :succeeded THEN 0
				ELSE reconciliation_failure_count + 1
			END,
			last_reconciliation_at = :attempted_at
		WHERE id = :id AND tenant_id = :tenant_id
		RETURNING *
	`, data, &run)
	return run, processingRunError(err)
}

// ListProcessingRunExecutions returns the expected model executions for a tenant-scoped run.
func (repository *InferenceProcessingRunRepository) ListProcessingRunExecutions(ctx context.Context, tenantID, processingRunID string) ([]entity.InferenceIngestionProcessingJob, error) {
	executions := make([]entity.InferenceIngestionProcessingJob, 0)
	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT jobs.* FROM ingestion_processing_jobs jobs
		JOIN ingestion_processing_runs runs ON runs.id = jobs.processing_run_id
		WHERE runs.tenant_id = :tenant_id AND runs.id = :processing_run_id
		ORDER BY jobs.created_at ASC, jobs.id ASC
	`, map[string]interface{}{"tenant_id": tenantID, "processing_run_id": processingRunID}, &executions)
	return executions, processingRunError(err)
}

// ListProcessingRunExecutionsByRunIDs loads all model executions for a bounded
// tenant-scoped history page in one query. Results are grouped by newest run and
// retain the frozen model-plan order within each run.
func (repository *InferenceProcessingRunRepository) ListProcessingRunExecutionsByRunIDs(ctx context.Context, data types.ListInferenceIngestionProcessingRunExecutions) ([]entity.InferenceIngestionProcessingJob, error) {
	executions := make([]entity.InferenceIngestionProcessingJob, 0)
	if len(data.ProcessingRunIDs) == 0 {
		return executions, nil
	}

	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT jobs.* FROM ingestion_processing_jobs jobs
		JOIN ingestion_processing_runs runs ON runs.id = jobs.processing_run_id
		WHERE runs.tenant_id = :tenant_id
		  AND runs.id = ANY(:processing_run_ids)
		ORDER BY runs.run_number DESC, jobs.created_at ASC, jobs.id ASC
	`, map[string]interface{}{
		"tenant_id": data.TenantID, "processing_run_ids": pq.Array(data.ProcessingRunIDs),
	}, &executions)
	return executions, processingRunError(err)
}

// SelectProcessingRunExecution returns one exact tenant/run/candidate/model execution.
func (repository *InferenceProcessingRunRepository) SelectProcessingRunExecution(ctx context.Context, tenantID, processingRunID, candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error) {
	var execution entity.InferenceIngestionProcessingJob
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(`
		SELECT jobs.* FROM ingestion_processing_jobs jobs
		JOIN ingestion_processing_runs runs ON runs.id = jobs.processing_run_id
		WHERE runs.tenant_id = :tenant_id
		  AND runs.id = :processing_run_id
		  AND jobs.candidate_id = :candidate_id
		  AND jobs.model_name = :model_name
	`, map[string]interface{}{
		"tenant_id": tenantID, "processing_run_id": processingRunID,
		"candidate_id": candidateID, "model_name": modelName,
	}, &execution)
	return execution, processingRunError(err)
}

// UpdateProcessingRunAggregate applies an optimistic versioned aggregate update.
func (repository *InferenceProcessingRunRepository) UpdateProcessingRunAggregate(ctx context.Context, data types.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error) {
	var run entity.InferenceIngestionProcessingRun
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(`
		UPDATE ingestion_processing_runs SET
			phase = :phase, outcome = :outcome,
			attention_required = :attention_required, attention_reasons = :attention_reasons,
			started_at = :started_at, completed_at = :completed_at, version = version + 1
		WHERE id = :id AND tenant_id = :tenant_id AND version = :expected_version
		RETURNING *
	`, data, &run)
	if !errors.Is(err, sql.ErrNoRows) {
		return run, processingRunError(err)
	}

	var existing entity.InferenceIngestionProcessingRun
	lookupErr := repository.PostgresSQLDBHandlerInterface.QueryRow(
		"SELECT * FROM ingestion_processing_runs WHERE id = :id AND tenant_id = :tenant_id",
		map[string]interface{}{"id": data.ID, "tenant_id": data.TenantID}, &existing,
	)
	if lookupErr == nil {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.DuplicateRecord)
	}
	return entity.InferenceIngestionProcessingRun{}, processingRunError(lookupErr)
}
