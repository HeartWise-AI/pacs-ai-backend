package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// expiredRunAttentionCodes are the attention reasons about executions still
// waiting; once nothing waits any more they no longer apply.
var expiredRunAttentionCodes = map[string]bool{
	entity.InferenceIngestionProcessingRunAttentionPendingStale:           true,
	entity.InferenceIngestionProcessingRunAttentionQueueStale:             true,
	entity.InferenceIngestionProcessingRunAttentionExpectedJobMissing:     true,
	entity.InferenceIngestionProcessingRunAttentionStudyServiceJobMissing: true,
}

// expirableAutomaticRunsQuery selects the runs created before
// :created_before that are not finished and either
//   - were started automatically (AUTO, LEGACY_IMPORT), have no running
//     execution, and have at least one pending or queued execution (with or
//     without a study-service job) not updated since :created_before; or
//   - have no execution at all and were started automatically or by a work
//     list reprocess: an empty run can never do anything.
//
// Oldest first, bounded.
const expirableAutomaticRunsQuery = `
	SELECT runs.* FROM ingestion_processing_runs runs
	WHERE runs.phase <> 'TERMINAL'
	  AND runs.created_at < :created_before
	  AND (
		(
			runs.run_trigger IN ('AUTO', 'LEGACY_IMPORT')
			AND NOT EXISTS (
				SELECT 1 FROM ingestion_processing_jobs jobs
				WHERE jobs.processing_run_id = runs.id
				  AND CAST(jobs.status AS text) = 'running'
			)
			AND EXISTS (
				SELECT 1 FROM ingestion_processing_jobs jobs
				WHERE jobs.processing_run_id = runs.id
				  AND CAST(jobs.status AS text) IN ('pending', 'queued')
				  AND jobs.updated_at < :created_before
			)
		)
		OR (
			runs.run_trigger IN ('AUTO', 'LEGACY_IMPORT', 'MANUAL_REPROCESS')
			AND NOT EXISTS (
				SELECT 1 FROM ingestion_processing_jobs jobs
				WHERE jobs.processing_run_id = runs.id
			)
		)
	  )
	ORDER BY runs.created_at ASC, runs.id ASC
	LIMIT :limit`

// ListNeverRanAutomaticProcessingRuns returns a bounded batch of old
// automatic runs with executions that are still waiting and can be expired.
func (repository *InferenceProcessingRunRepository) ListNeverRanAutomaticProcessingRuns(
	_ context.Context,
	data types.ListNeverRanAutomaticProcessingRuns,
) ([]entity.InferenceIngestionProcessingRun, error) {
	runs := make([]entity.InferenceIngestionProcessingRun, 0)
	if data.CreatedBefore.IsZero() || data.Limit <= 0 {
		return nil, errors.New(apiError.InvalidPayload)
	}
	err := repository.PostgresSQLDBHandlerInterface.Query(expirableAutomaticRunsQuery, map[string]interface{}{
		"created_before": data.CreatedBefore, "limit": data.Limit,
	}, &runs)
	return runs, processingRunError(err)
}

// ExpireNeverRanProcessingRun closes the waiting executions of one old
// automatic run. Under row locks it re-checks the run (a callback may have
// arrived since it was listed), marks each pending or queued execution not
// updated since CreatedBefore as skipped with the EXPIRED_NEVER_RAN reason,
// leaves completed, failed, skipped and cancelled executions untouched, and
// stores the run aggregate (terminal when nothing else waits, with the usual
// outcome). A run with a running execution is left alone. Nothing is
// deleted; a run with nothing left to expire is reported unchanged, which
// makes the call idempotent.
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
	if len(executions) == 0 {
		if reason := expirableEmptyRun(run, data.CreatedBefore); reason != "" {
			result.NotExpiredReason = reason
			return result, nil
		}
		// No execution to aggregate: the run is closed as it is, without a result.
		if err = tx.GetContext(ctx, &run, `
			UPDATE ingestion_processing_runs SET
				phase = $1, outcome = $2,
				attention_required = FALSE, attention_reasons = '[]',
				completed_at = COALESCE(completed_at, $3), version = version + 1
			WHERE id = $4 AND tenant_id = $5
			RETURNING *
		`, entity.InferenceIngestionProcessingRunPhaseTerminal, entity.InferenceIngestionProcessingRunOutcomeNoResult,
			data.ExpiredAt, data.ID, data.TenantID); err != nil {
			return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
		}
		if err = tx.Commit(); err != nil {
			return types.ExpireNeverRanProcessingRunResult{}, processingRunError(err)
		}
		return types.ExpireNeverRanProcessingRunResult{Expired: true, Empty: true, Run: run}, nil
	}
	targets, reason := expirableExecutions(run, executions, data.CreatedBefore)
	if reason != "" {
		result.NotExpiredReason = reason
		return result, nil
	}

	code := entity.InferenceIngestionProcessingJobSkipReasonExpiredNeverRan
	for _, index := range targets {
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

	// The waiting-execution attention reasons no longer apply once nothing waits.
	stillWaiting := false
	for index := range executions {
		stillWaiting = stillWaiting || !executions[index].Status.IsTerminal()
	}
	if !stillWaiting {
		kept := make(entity.InferenceIngestionProcessingRunAttentionReasons, 0, len(run.AttentionReasons))
		for _, attention := range run.AttentionReasons {
			if !expiredRunAttentionCodes[attention.Code] {
				kept = append(kept, attention)
			}
		}
		if len(kept) != len(run.AttentionReasons) {
			run.AttentionReasons = kept
			run.AttentionRequired = len(kept) > 0
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
	return types.ExpireNeverRanProcessingRunResult{Expired: true, Executions: len(targets), Run: run}, nil
}

// expirableEmptyRun applies the rules for a run without executions and
// returns why it must be left alone, or "".
func expirableEmptyRun(run entity.InferenceIngestionProcessingRun, createdBefore time.Time) string {
	switch {
	case run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal:
		return "run is finished"
	case run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerAuto &&
		run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerLegacyImport &&
		run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerManualReprocess:
		return "run was started from the viewer"
	case !run.CreatedAt.Before(createdBefore):
		return "run is not old enough"
	}
	return ""
}

// expirableExecutions applies the rules to the locked rows: the indexes of
// the executions to expire, or why the run must be left alone.
func expirableExecutions(
	run entity.InferenceIngestionProcessingRun,
	executions []entity.InferenceIngestionProcessingJob,
	createdBefore time.Time,
) ([]int, string) {
	switch {
	case run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal:
		return nil, "run is finished"
	case run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerAuto &&
		run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerLegacyImport:
		return nil, "run was not started automatically"
	case !run.CreatedAt.Before(createdBefore):
		return nil, "run is not old enough"
	}
	targets := make([]int, 0, len(executions))
	for index := range executions {
		execution := &executions[index]
		switch execution.Status {
		case entity.InferenceIngestionProcessingJobStatusRunning:
			return nil, "an execution is running"
		case entity.InferenceIngestionProcessingJobStatusPending, entity.InferenceIngestionProcessingJobStatusQueued:
			if execution.UpdatedAt.Before(createdBefore) {
				targets = append(targets, index)
			}
		}
	}
	if len(targets) == 0 {
		return nil, "no waiting execution old enough"
	}
	return targets, ""
}
