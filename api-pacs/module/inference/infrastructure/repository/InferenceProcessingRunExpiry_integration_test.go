package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// expiryIntegrationHandler runs statements on the database itself, because
// the expiry opens its own transaction.
type expiryIntegrationHandler struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
	database *sqlx.DB
}

func (handler *expiryIntegrationHandler) Begin() (*sqlx.Tx, error) { return handler.database.Beginx() }

func (handler *expiryIntegrationHandler) Query(statement string, model interface{}, target interface{}) error {
	prepared, err := handler.database.PrepareNamed(statement)
	if err != nil {
		return err
	}
	defer prepared.Close()
	return prepared.Select(target, model)
}

// TestAutoRunExpiryAgainstPostgreSQL checks the selection and the closing of
// automatic runs that never ran on a migrated database. All identifiers are
// synthetic; the fixture rows are removed at the end.
func TestAutoRunExpiryAgainstPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("WORKLIST_INTEGRATION_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("WORKLIST_INTEGRATION_DATABASE_URL is not set")
	}
	database, err := sqlx.Connect("pgx", databaseURL)
	require.NoError(t, err)
	defer database.Close()
	ctx := context.Background()
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &expiryIntegrationHandler{database: database}}

	const tenant = "exp-tenant"
	cleanup := func() {
		for _, statement := range []string{
			`DELETE FROM ingestion_processing_jobs WHERE tenant_id = $1`,
			`DELETE FROM ingestion_processing_runs WHERE tenant_id = $1`,
			`DELETE FROM ingestion_candidates WHERE tenant_id = $1`,
			`DELETE FROM ingestion_jobs WHERE tenant_id = $1`,
		} {
			_, _ = database.Exec(statement, tenant)
		}
	}
	cleanup()
	defer cleanup() // runs before the deferred Close

	now := time.Now().UTC().Truncate(time.Microsecond)
	createdBefore := now.Add(-720 * time.Hour)
	_, err = database.Exec(`
		INSERT INTO ingestion_jobs (
			id, tenant_id, dicom_modality, container_id, model_id, model_name,
			model_version, modalities, stability_minutes, recent_window_minutes,
			missing_polls_threshold, schedule_start_timestamp,
			schedule_end_timestamp, status, created_at, updated_at
		) VALUES ('exp-job', $1, 'XA', 'container', 'model', 'model', 'v1',
			ARRAY['XA'], 1, 60, 3, $2, $3, 'RUNNING', $2, $2)
	`, tenant, now, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = database.Exec(`
		INSERT INTO ingestion_candidates (
			id, tenant_id, ingestion_job_id, study_instance_uid, first_seen_at, last_seen_at,
			last_changed_at, missing_polls, status, created_at, updated_at
		) VALUES ('exp-cand', $1, 'exp-job', '7.1', $2, $2, $2, 0, 'RETRIEVED', $2, $2)
	`, tenant, now)
	require.NoError(t, err)

	type job struct {
		status            string
		studyServiceJobID interface{}
	}
	type run struct {
		id, trigger, phase string
		at                 time.Time
		jobs               []job
	}
	never := job{"pending", nil}
	runs := []run{
		{"exp-auto-old", "AUTO", "QUEUED", now.Add(-800 * time.Hour), []job{never, {"queued", nil}, {"queued", " "}}},
		{"exp-legacy-old", "LEGACY_IMPORT", "QUEUED", now.Add(-900 * time.Hour), []job{never}},
		{"exp-viewer-old", "VIEWER_REQUEST", "QUEUED", now.Add(-800 * time.Hour), []job{never}},
		{"exp-manual-old", "MANUAL_REPROCESS", "QUEUED", now.Add(-800 * time.Hour), []job{never}},
		{"exp-auto-young", "AUTO", "QUEUED", now.Add(-700 * time.Hour), []job{never}},
		{"exp-auto-boundary", "AUTO", "QUEUED", createdBefore, []job{never}},
		{"exp-auto-partial", "AUTO", "QUEUED", now.Add(-800 * time.Hour), []job{{"completed", "ss-1"}, never}},
		{"exp-auto-gateway-skip", "AUTO", "QUEUED", now.Add(-800 * time.Hour), []job{{"skipped", nil}, never}},
		{"exp-auto-running", "AUTO", "PROCESSING", now.Add(-800 * time.Hour), []job{{"running", "ss-2"}, never}},
		{"exp-auto-dispatched", "AUTO", "QUEUED", now.Add(-800 * time.Hour), []job{{"queued", "ss-3"}}},
		{"exp-auto-terminal", "AUTO", "TERMINAL", now.Add(-800 * time.Hour), []job{{"cancelled", nil}}},
		{"exp-auto-empty", "AUTO", "QUEUED", now.Add(-800 * time.Hour), nil},
	}
	for runIndex, fixture := range runs {
		_, err = database.Exec(`
			INSERT INTO ingestion_processing_runs (
				id, tenant_id, study_instance_uid, run_number, run_trigger, phase,
				attention_required, attention_reasons, version, created_at, updated_at
			) VALUES ($1, $2, $3, 1, $4, $5, FALSE, '[]', 1, $6, $6)
		`, fixture.id, tenant, fmt.Sprintf("7.%d", runIndex+1), fixture.trigger, fixture.phase, fixture.at)
		require.NoError(t, err)
		for jobIndex, execution := range fixture.jobs {
			_, err = database.Exec(`
				INSERT INTO ingestion_processing_jobs (
					id, processing_run_id, candidate_id, tenant_id, model_name, status,
					study_service_job_id, created_at, updated_at
				) VALUES ($1, $2, 'exp-cand', $3, $4, $5, $6, $7, $7)
			`, fixture.id+"-"+string(rune('a'+jobIndex)), fixture.id, tenant, "model-"+string(rune('a'+jobIndex)),
				execution.status, execution.studyServiceJobID, fixture.at)
			require.NoError(t, err)
		}
	}
	countRows := func() (int, int) {
		var runCount, jobCount int
		require.NoError(t, database.Get(&runCount, `SELECT COUNT(*) FROM ingestion_processing_runs WHERE tenant_id = $1`, tenant))
		require.NoError(t, database.Get(&jobCount, `SELECT COUNT(*) FROM ingestion_processing_jobs WHERE tenant_id = $1`, tenant))
		return runCount, jobCount
	}
	runsBefore, jobsBefore := countRows()

	list := func(limit int) []string {
		t.Helper()
		selected, err := repository.ListNeverRanAutomaticProcessingRuns(ctx, types.ListNeverRanAutomaticProcessingRuns{
			CreatedBefore: createdBefore, Limit: limit,
		})
		require.NoError(t, err)
		ids := []string{}
		for _, run := range selected {
			if run.TenantID == tenant {
				ids = append(ids, run.ID)
			}
		}
		return ids
	}
	require.Equal(t, []string{"exp-legacy-old", "exp-auto-old"}, list(200),
		"automatic, older than the limit, every execution never ran; oldest first")
	bounded, err := repository.ListNeverRanAutomaticProcessingRuns(ctx, types.ListNeverRanAutomaticProcessingRuns{
		CreatedBefore: createdBefore, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, bounded, 1, "bounded batch")

	expire := func(id string) types.ExpireNeverRanProcessingRunResult {
		t.Helper()
		result, err := repository.ExpireNeverRanProcessingRun(ctx, types.ExpireNeverRanProcessingRun{
			ID: id, TenantID: tenant, CreatedBefore: createdBefore,
			Message: "Expired: never ran within 30 days (automatic)", ExpiredAt: now,
		})
		require.NoError(t, err)
		return result
	}

	// The re-check under lock refuses what the selection leaves out.
	for id, reason := range map[string]string{
		"exp-viewer-old":        "run was not started automatically",
		"exp-manual-old":        "run was not started automatically",
		"exp-auto-young":        "run is not old enough",
		"exp-auto-boundary":     "run is not old enough",
		"exp-auto-partial":      "an execution was dispatched, started or finished",
		"exp-auto-gateway-skip": "an execution was dispatched, started or finished",
		"exp-auto-running":      "an execution was dispatched, started or finished",
		"exp-auto-dispatched":   "an execution was dispatched, started or finished",
		"exp-auto-terminal":     "run is finished",
		"exp-auto-empty":        "run has no executions",
	} {
		result := expire(id)
		require.False(t, result.Expired, id)
		require.Equal(t, reason, result.NotExpiredReason, id)
	}

	result := expire("exp-auto-old")
	require.True(t, result.Expired)
	require.Equal(t, 3, result.Executions)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, result.Run.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomeNoResult, *result.Run.Outcome)
	require.EqualValues(t, 2, result.Run.Version)

	executions, err := (&InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &expiryIntegrationHandler{database: database}}).
		ListProcessingRunExecutions(ctx, tenant, "exp-auto-old")
	require.NoError(t, err)
	require.Len(t, executions, 3)
	for _, execution := range executions {
		require.Equal(t, entity.InferenceIngestionProcessingJobStatusSkipped, execution.Status)
		require.True(t, execution.IsExpiredNeverRan())
		require.Equal(t, "Expired: never ran within 30 days (automatic)", *execution.SkipReasonMessage)
		require.NotNil(t, execution.CompletedAt)
	}

	// Idempotent: a second pass changes nothing.
	again := expire("exp-auto-old")
	require.False(t, again.Expired)
	require.Equal(t, "run is finished", again.NotExpiredReason)
	require.EqualValues(t, 2, again.Run.Version)
	require.Equal(t, []string{"exp-legacy-old"}, list(200))
	require.True(t, expire("exp-legacy-old").Expired)
	require.Empty(t, list(200))

	// Untouched runs keep their state, and nothing was deleted.
	var untouched []string
	require.NoError(t, database.Select(&untouched, `
		SELECT DISTINCT processing_run_id FROM ingestion_processing_jobs
		WHERE tenant_id = $1 AND skip_reason_code = 'EXPIRED_NEVER_RAN' ORDER BY 1`, tenant))
	require.Equal(t, []string{"exp-auto-old", "exp-legacy-old"}, untouched)
	runsAfter, jobsAfter := countRows()
	require.Equal(t, runsBefore, runsAfter)
	require.Equal(t, jobsBefore, jobsAfter)
}
