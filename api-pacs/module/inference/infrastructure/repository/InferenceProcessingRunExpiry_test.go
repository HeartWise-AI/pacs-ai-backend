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

func TestNeverRanAutomaticRunsQuerySelectsOnlyAutomaticRunsThatNeverRan(t *testing.T) {
	for _, fragment := range []string{
		"runs.phase <> 'TERMINAL'",
		"runs.run_trigger IN ('AUTO', 'LEGACY_IMPORT')",
		"runs.created_at < :created_before",
		"CAST(jobs.status AS text) IN ('pending', 'queued')",
		"NULLIF(BTRIM(jobs.study_service_job_id), '') IS NULL",
		"ORDER BY runs.created_at ASC, runs.id ASC",
		"LIMIT :limit",
	} {
		require.Contains(t, neverRanAutomaticRunsQuery, fragment)
	}
	require.NotContains(t, neverRanAutomaticRunsQuery, "DELETE")

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{}}
	_, err := repository.ListNeverRanAutomaticProcessingRuns(context.Background(), types.ListNeverRanAutomaticProcessingRuns{Limit: 1})
	require.EqualError(t, err, apiError.InvalidPayload)
	_, err = repository.ExpireNeverRanProcessingRun(context.Background(), types.ExpireNeverRanProcessingRun{ID: "run", TenantID: "t"})
	require.EqualError(t, err, apiError.InvalidPayload)
}

func TestNeverRanExpiryRefusal(t *testing.T) {
	createdBefore := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	data := types.ExpireNeverRanProcessingRun{CreatedBefore: createdBefore}
	run := entity.InferenceIngestionProcessingRun{
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto, Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
		CreatedAt: createdBefore.Add(-time.Second),
	}
	pending := entity.InferenceIngestionProcessingJob{Status: entity.InferenceIngestionProcessingJobStatusPending}
	running := entity.InferenceIngestionProcessingJob{Status: entity.InferenceIngestionProcessingJobStatusRunning}
	require.Equal(t, "", neverRanExpiryRefusal(run, []entity.InferenceIngestionProcessingJob{pending}, data))

	legacy := run
	legacy.RunTrigger = entity.InferenceIngestionProcessingRunTriggerLegacyImport
	require.Equal(t, "", neverRanExpiryRefusal(legacy, []entity.InferenceIngestionProcessingJob{pending}, data))
	for _, trigger := range []entity.InferenceIngestionProcessingRunTrigger{
		entity.InferenceIngestionProcessingRunTriggerViewerRequest, entity.InferenceIngestionProcessingRunTriggerManualReprocess,
	} {
		manual := run
		manual.RunTrigger = trigger
		require.Equal(t, "run was not started automatically", neverRanExpiryRefusal(manual, []entity.InferenceIngestionProcessingJob{pending}, data))
	}
	atLimit := run
	atLimit.CreatedAt = createdBefore
	require.Equal(t, "run is not old enough", neverRanExpiryRefusal(atLimit, []entity.InferenceIngestionProcessingJob{pending}, data))
	require.Equal(t, "an execution was dispatched, started or finished",
		neverRanExpiryRefusal(run, []entity.InferenceIngestionProcessingJob{pending, running}, data))
	require.Equal(t, "run has no executions", neverRanExpiryRefusal(run, nil, data))
	terminal := run
	terminal.Phase = entity.InferenceIngestionProcessingRunPhaseTerminal
	require.Equal(t, "run is finished", neverRanExpiryRefusal(terminal, []entity.InferenceIngestionProcessingJob{pending}, data))
}
