package repository

import (
	"context"
	"errors"
	"strings"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// neverRanAutomaticRunsQuery selects automatic runs (AUTO, LEGACY_IMPORT)
// created before :created_before that are not finished, have at least one
// execution, and whose executions are all pending or queued without a
// study-service job. A run with any execution that started, finished or was
// handed to study-service is never selected. Oldest first, bounded.
const neverRanAutomaticRunsQuery = `
	SELECT runs.* FROM ingestion_processing_runs runs
	WHERE runs.phase <> 'TERMINAL'
	  AND runs.run_trigger IN ('AUTO', 'LEGACY_IMPORT')
	  AND runs.created_at < :created_before
	  AND EXISTS (
		SELECT 1 FROM ingestion_processing_jobs jobs
		WHERE jobs.processing_run_id = runs.id
	  )
	  AND NOT EXISTS (
		SELECT 1 FROM ingestion_processing_jobs jobs
		WHERE jobs.processing_run_id = runs.id
		  AND NOT (
			CAST(jobs.status AS text) IN ('pending', 'queued')
			AND NULLIF(BTRIM(jobs.study_service_job_id), '') IS NULL
		  )
	  )
	ORDER BY runs.created_at ASC, runs.id ASC
	LIMIT :limit`

// ListNeverRanAutomaticProcessingRuns returns a bounded batch of automatic
// runs that never ran and are older than the expiry.
func (repository *InferenceProcessingRunRepository) ListNeverRanAutomaticProcessingRuns(
	_ context.Context,
	data types.ListNeverRanAutomaticProcessingRuns,
) ([]entity.InferenceIngestionProcessingRun, error) {
	runs := make([]entity.InferenceIngestionProcessingRun, 0)
	if data.CreatedBefore.IsZero() || data.Limit <= 0 {
		return nil, errors.New(apiError.InvalidPayload)
	}
	err := repository.PostgresSQLDBHandlerInterface.Query(neverRanAutomaticRunsQuery, map[string]interface{}{
		"created_before": data.CreatedBefore, "limit": data.Limit,
	}, &runs)
	return runs, processingRunError(err)
}

// ExpireNeverRanProcessingRun closes one automatic run that never ran. Under
// row locks it re-checks every selection rule (a dispatch may have happened
// since the run was listed), marks each execution skipped with the
// EXPIRED_NEVER_RAN reason, and stores the run aggregate, which is then
// terminal. Nothing is deleted. A run that no longer qualifies is left
// unchanged and reported with the reason, which makes the call idempotent.
func (repository *InferenceProcessingRunRepository) ExpireNeverRanProcessingRun(
	ctx context.Context,
	data types.ExpireNeverRanProcessingRun,
) (types.ExpireNeverRanProcessingRunResult, error) {
	if strings.TrimSpace(data.ID) == "" || strings.TrimSpace(data.TenantID) == "" ||
		data.CreatedBefore.IsZero() || data.ExpiredAt.IsZero() || strings.TrimSpace(data.Message) == "" {
		return types.ExpireNeverRanProcessingRunResult{}, errors.New(apiError.InvalidPayload)
	}
	tx, err := repository.PostgresSQLDBHandlerInterface.Begin()
	if err != nil {
		return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
	}
	defer tx.Rollback()

	var run entity.InferenceIngestionProcessingRun
	if err = tx.GetContext(ctx, &run, `
		SELECT * FROM ingestion_processing_runs
		WHERE id = $1 AND tenant_id = $2
		FOR UPDATE
	`, data.ID, data.TenantID); err != nil {
		return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
	}
	executions := make([]entity.InferenceIngestionProcessingJob, 0)
	if err = tx.SelectContext(ctx, &executions, `
		SELECT * FROM ingestion_processing_jobs
		WHERE processing_run_id = $1
		ORDER BY created_at ASC, id ASC
		FOR UPDATE
	`, data.ID); err != nil {
		return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
	}

	result := types.ExpireNeverRanProcessingRunResult{Run: run}
	if reason := neverRanExpiryRefusal(run, executions, data); reason != "" {
		result.NotExpiredReason = reason
		return result, nil
	}

	code := entity.InferenceIngestionProcessingJobSkipReasonExpiredNeverRan
	for index := range executions {
		if err = tx.GetContext(ctx, &executions[index], `
			UPDATE ingestion_processing_jobs SET
				status = $1,
				skip_reason_code = $2,
				skip_reason_message = $3,
				completed_at = COALESCE(completed_at, $4)
			WHERE id = $5 AND processing_run_id = $6
			RETURNING *
		`, entity.InferenceIngestionProcessingJobStatusSkipped, code, data.Message, data.ExpiredAt,
			executions[index].ID, data.ID); err != nil {
			return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
		}
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
		data.ID, data.TenantID); err != nil {
		return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
	}
	if err = tx.Commit(); err != nil {
		return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
	}
	return types.ExpireNeverRanProcessingRunResult{Expired: true, Executions: len(executions), Run: run}, nil
}

// neverRanExpiryRefusal applies the selection rules to the locked rows and
// returns why the run must not be expired, or "".
func neverRanExpiryRefusal(
	run entity.InferenceIngestionProcessingRun,
	executions []entity.InferenceIngestionProcessingJob,
	data types.ExpireNeverRanProcessingRun,
) string {
	switch {
	case run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal:
		return "run is finished"
	case run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerAuto &&
		run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerLegacyImport:
		return "run was not started automatically"
	case !run.CreatedAt.Before(data.CreatedBefore):
		return "run is not old enough"
	case len(executions) == 0:
		return "run has no executions"
	}
	for index := range executions {
		if !executions[index].NeverRan() {
			return "an execution was dispatched, started or finished"
		}
	}
	return ""
}
