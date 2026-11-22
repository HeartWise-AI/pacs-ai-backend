package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func TestExpirableAutomaticRunsQuerySelectsOldAutomaticRunsWithWaitingExecutions(t *testing.T) {
	for _, fragment := range []string{
		"runs.phase <> 'TERMINAL'",
		"runs.run_trigger IN ('AUTO', 'LEGACY_IMPORT')",
		"runs.created_at < :created_before",
		"CAST(jobs.status AS text) = 'running'",
		"CAST(jobs.status AS text) IN ('pending', 'queued')",
		"jobs.updated_at < :created_before",
		"runs.run_trigger IN ('AUTO', 'LEGACY_IMPORT', 'MANUAL_REPROCESS')",
		"ORDER BY runs.created_at ASC, runs.id ASC",
		"LIMIT :limit",
	} {
		require.Contains(t, expirableAutomaticRunsQuery, fragment)
	}
	require.NotContains(t, expirableAutomaticRunsQuery, "DELETE")
	require.NotContains(t, expirableAutomaticRunsQuery, "study_service_job_id",
		"a queued execution with a study-service job expires too")

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{}}
	_, err := repository.ListNeverRanAutomaticProcessingRuns(context.Background(), types.ListNeverRanAutomaticProcessingRuns{Limit: 1})
	require.EqualError(t, err, apiError.InvalidPayload)
	_, err = repository.ExpireNeverRanProcessingRun(context.Background(), types.ExpireNeverRanProcessingRun{ID: "run", TenantID: "t"})
	require.EqualError(t, err, apiError.InvalidPayload)
}

func TestExpirableEmptyRun(t *testing.T) {
	createdBefore := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	run := entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseQueued, CreatedAt: createdBefore.Add(-time.Second)}
	for _, trigger := range []entity.InferenceIngestionProcessingRunTrigger{
		entity.InferenceIngestionProcessingRunTriggerAuto, entity.InferenceIngestionProcessingRunTriggerLegacyImport,
		entity.InferenceIngestionProcessingRunTriggerManualReprocess,
	} {
		run.RunTrigger = trigger
		require.Empty(t, expirableEmptyRun(run, createdBefore), trigger)
	}
	run.RunTrigger = entity.InferenceIngestionProcessingRunTriggerViewerRequest
	require.Equal(t, "run was started from the viewer", expirableEmptyRun(run, createdBefore))
	run.RunTrigger = entity.InferenceIngestionProcessingRunTriggerAuto
	young := run
	young.CreatedAt = createdBefore
	require.Equal(t, "run is not old enough", expirableEmptyRun(young, createdBefore))
	terminal := run
	terminal.Phase = entity.InferenceIngestionProcessingRunPhaseTerminal
	require.Equal(t, "run is finished", expirableEmptyRun(terminal, createdBefore))
}

func TestExpirableExecutions(t *testing.T) {
	createdBefore := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	longAgo, recently := createdBefore.Add(-time.Hour), createdBefore.Add(time.Hour)
	run := entity.InferenceIngestionProcessingRun{
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto, Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
		CreatedAt: createdBefore.Add(-time.Second),
	}
	job := func(status entity.InferenceIngestionProcessingJobStatus, updated time.Time, studyServiceJobID string) entity.InferenceIngestionProcessingJob {
		execution := entity.InferenceIngestionProcessingJob{Status: status, UpdatedAt: updated}
		if studyServiceJobID != "" {
			execution.StudyServiceJobID = &studyServiceJobID
		}
		return execution
	}
	completed := job(entity.InferenceIngestionProcessingJobStatusCompleted, longAgo, "ss-1")
	pending := job(entity.InferenceIngestionProcessingJobStatusPending, longAgo, "")
	queuedDispatched := job(entity.InferenceIngestionProcessingJobStatusQueued, longAgo, "ss-lost")
	queuedRecent := job(entity.InferenceIngestionProcessingJobStatusQueued, recently, "ss-2")
	running := job(entity.InferenceIngestionProcessingJobStatusRunning, longAgo, "ss-3")

	targets, reason := expirableExecutions(run, []entity.InferenceIngestionProcessingJob{completed, pending, queuedDispatched, queuedRecent}, createdBefore)
	require.Empty(t, reason)
	require.Equal(t, []int{1, 2}, targets, "completed and recently updated executions stay")

	legacy := run
	legacy.RunTrigger = entity.InferenceIngestionProcessingRunTriggerLegacyImport
	targets, _ = expirableExecutions(legacy, []entity.InferenceIngestionProcessingJob{pending}, createdBefore)
	require.Equal(t, []int{0}, targets)

	for _, trigger := range []entity.InferenceIngestionProcessingRunTrigger{
		entity.InferenceIngestionProcessingRunTriggerViewerRequest, entity.InferenceIngestionProcessingRunTriggerManualReprocess,
	} {
		manual := run
		manual.RunTrigger = trigger
		_, reason = expirableExecutions(manual, []entity.InferenceIngestionProcessingJob{pending}, createdBefore)
		require.Equal(t, "run was not started automatically", reason)
	}
	atLimit := run
	atLimit.CreatedAt = createdBefore
	_, reason = expirableExecutions(atLimit, []entity.InferenceIngestionProcessingJob{pending}, createdBefore)
	require.Equal(t, "run is not old enough", reason)
	_, reason = expirableExecutions(run, []entity.InferenceIngestionProcessingJob{pending, running}, createdBefore)
	require.Equal(t, "an execution is running", reason)
	_, reason = expirableExecutions(run, []entity.InferenceIngestionProcessingJob{completed, queuedRecent}, createdBefore)
	require.Equal(t, "no waiting execution old enough", reason)
	atBoundary := job(entity.InferenceIngestionProcessingJobStatusPending, createdBefore, "")
	_, reason = expirableExecutions(run, []entity.InferenceIngestionProcessingJob{atBoundary}, createdBefore)
	require.Equal(t, "no waiting execution old enough", reason)
	terminal := run
	terminal.Phase = entity.InferenceIngestionProcessingRunPhaseTerminal
	_, reason = expirableExecutions(terminal, []entity.InferenceIngestionProcessingJob{pending}, createdBefore)
	require.Equal(t, "run is finished", reason)
}
