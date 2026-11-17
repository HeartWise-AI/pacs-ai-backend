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
		// updated is how long ago the execution last changed; zero means
		// when the run was created.
		updated time.Duration
	}
	type run struct {
		id, trigger, phase string
		at                 time.Time
		attention          string
		jobs               []job
	}
	pending := job{status: "pending"}
	old := now.Add(-800 * time.Hour)
	missingJob := `[{"code":"STUDY_SERVICE_JOB_MISSING"},{"code":"DISPATCH_FAILED"}]`
	runs := []run{
		// Partial progress: completed + pending + queued with a study-service
		// job study-service no longer knows.
		{"exp-auto-partial", "AUTO", "QUEUED", old, "", []job{
			{status: "completed", studyServiceJobID: "ss-1"}, pending, {status: "queued", studyServiceJobID: "ss-lost"}}},
		{"exp-auto-failed", "AUTO", "QUEUED", old.Add(time.Minute), "", []job{
			{status: "completed", studyServiceJobID: "ss-2"}, {status: "failed"}, pending}},
		{"exp-legacy-old", "LEGACY_IMPORT", "QUEUED", now.Add(-900 * time.Hour), "", []job{pending}},
		{"exp-auto-attention", "AUTO", "QUEUED", old.Add(2 * time.Minute), missingJob, []job{
			{status: "queued", studyServiceJobID: "ss-3"}}},
		// One waiting execution changed recently: only the old one expires.
		{"exp-auto-mixed", "AUTO", "QUEUED", old.Add(3 * time.Minute), "", []job{
			pending, {status: "queued", studyServiceJobID: "ss-4", updated: 10 * time.Hour}}},
		{"exp-auto-recent-callback", "AUTO", "QUEUED", old, "", []job{
			{status: "queued", studyServiceJobID: "ss-5", updated: 10 * time.Hour}}},
		{"exp-auto-running", "AUTO", "PROCESSING", old, "", []job{{status: "running", studyServiceJobID: "ss-6"}, pending}},
		{"exp-viewer-old", "VIEWER_REQUEST", "QUEUED", old, "", []job{pending}},
		{"exp-manual-old", "MANUAL_REPROCESS", "QUEUED", old, "", []job{{status: "completed"}, pending}},
		{"exp-auto-young", "AUTO", "QUEUED", now.Add(-700 * time.Hour), "", []job{pending}},
		{"exp-auto-boundary", "AUTO", "QUEUED", createdBefore, "", []job{pending}},
		{"exp-auto-terminal", "AUTO", "TERMINAL", old, "", []job{{status: "cancelled"}}},
		{"exp-auto-empty", "AUTO", "QUEUED", old, "", nil},
	}
	for runIndex, fixture := range runs {
		attention := fixture.attention
		if attention == "" {
			attention = "[]"
		}
		_, err = database.Exec(`
			INSERT INTO ingestion_processing_runs (
				id, tenant_id, study_instance_uid, run_number, run_trigger, phase,
				attention_required, attention_reasons, version, created_at, updated_at
			) VALUES ($1, $2, $3, 1, $4, $5, $6, $7, 1, $8, $8)
		`, fixture.id, tenant, fmt.Sprintf("7.%d", runIndex+1), fixture.trigger, fixture.phase,
			attention != "[]", attention, fixture.at)
		require.NoError(t, err)
		for jobIndex, execution := range fixture.jobs {
			updated := fixture.at
			if execution.updated > 0 {
				updated = now.Add(-execution.updated)
			}
			_, err = database.Exec(`
				INSERT INTO ingestion_processing_jobs (
					id, processing_run_id, candidate_id, tenant_id, model_name, status,
					study_service_job_id, created_at, updated_at
				) VALUES ($1, $2, 'exp-cand', $3, $4, $5, $6, $7, $8)
			`, fixture.id+"-"+string(rune('a'+jobIndex)), fixture.id, tenant, "model-"+string(rune('a'+jobIndex)),
				execution.status, execution.studyServiceJobID, fixture.at, updated)
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
	require.Equal(t, []string{
		"exp-legacy-old", "exp-auto-partial", "exp-auto-failed", "exp-auto-attention", "exp-auto-mixed",
	}, list(200), "automatic, old enough, nothing running, something waiting for long; oldest first")
	bounded, err := repository.ListNeverRanAutomaticProcessingRuns(ctx, types.ListNeverRanAutomaticProcessingRuns{
		CreatedBefore: createdBefore, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, bounded, 1, "bounded batch")

	const message = "Expired: did not complete within 30 days (automatic)"
	expire := func(id string) types.ExpireNeverRanProcessingRunResult {
		t.Helper()
		result, err := repository.ExpireNeverRanProcessingRun(ctx, types.ExpireNeverRanProcessingRun{
			ID: id, TenantID: tenant, CreatedBefore: createdBefore, Message: message, ExpiredAt: now,
		})
		require.NoError(t, err)
		return result
	}
	statuses := func(id string) []string {
		t.Helper()
		var values []string
		require.NoError(t, database.Select(&values, `
			SELECT CAST(status AS text) || COALESCE('/' || skip_reason_code, '')
			FROM ingestion_processing_jobs WHERE processing_run_id = $1 ORDER BY id`, id))
		return values
	}

	// The re-check under lock refuses what the selection leaves out.
	for id, reason := range map[string]string{
		"exp-viewer-old":           "run was not started automatically",
		"exp-manual-old":           "run was not started automatically",
		"exp-auto-young":           "run is not old enough",
		"exp-auto-boundary":        "run is not old enough",
		"exp-auto-running":         "an execution is running",
		"exp-auto-recent-callback": "no waiting execution old enough",
		"exp-auto-terminal":        "run is finished",
		"exp-auto-empty":           "no waiting execution old enough",
	} {
		result := expire(id)
		require.False(t, result.Expired, id)
		require.Equal(t, reason, result.NotExpiredReason, id)
	}
	require.Equal(t, []string{"pending"}, statuses("exp-viewer-old"))
	require.Equal(t, []string{"completed", "pending"}, statuses("exp-manual-old"))
	require.Equal(t, []string{"running", "pending"}, statuses("exp-auto-running"))

	// Partial progress: the completed execution stays, the waiting ones expire.
	result := expire("exp-auto-partial")
	require.True(t, result.Expired)
	require.Equal(t, 2, result.Executions)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, result.Run.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomeSuccessWithSkips, *result.Run.Outcome)
	require.EqualValues(t, 2, result.Run.Version)
	require.Equal(t, []string{"completed", "skipped/EXPIRED_NEVER_RAN", "skipped/EXPIRED_NEVER_RAN"}, statuses("exp-auto-partial"))
	executions, err := repository.ListProcessingRunExecutions(ctx, tenant, "exp-auto-partial")
	require.NoError(t, err)
	for _, execution := range executions {
		if execution.IsExpiredNeverRan() {
			require.Equal(t, message, *execution.SkipReasonMessage)
			require.NotNil(t, execution.CompletedAt)
		}
	}
	require.Equal(t, "ss-lost", *executions[2].StudyServiceJobID, "the study-service job id is kept for the audit trail")

	failed := expire("exp-auto-failed")
	require.True(t, failed.Expired)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *failed.Run.Outcome)
	require.Equal(t, []string{"completed", "failed", "skipped/EXPIRED_NEVER_RAN"}, statuses("exp-auto-failed"))

	legacy := expire("exp-legacy-old")
	require.True(t, legacy.Expired)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomeNoResult, *legacy.Run.Outcome)

	// The waiting-execution attention reasons go with the waiting executions.
	attention := expire("exp-auto-attention")
	require.True(t, attention.Expired)
	require.Equal(t, []string{entity.InferenceIngestionProcessingRunAttentionDispatchFailed},
		[]string{attention.Run.AttentionReasons[0].Code})
	require.Len(t, attention.Run.AttentionReasons, 1)
	require.True(t, attention.Run.AttentionRequired)

	// Only the old waiting execution expires; the run keeps waiting for the other.
	mixed := expire("exp-auto-mixed")
	require.True(t, mixed.Expired)
	require.Equal(t, 1, mixed.Executions)
	require.NotEqual(t, entity.InferenceIngestionProcessingRunPhaseTerminal, mixed.Run.Phase)
	require.Equal(t, []string{"skipped/EXPIRED_NEVER_RAN", "queued"}, statuses("exp-auto-mixed"))

	// Idempotent: a second pass changes nothing.
	again := expire("exp-auto-partial")
	require.False(t, again.Expired)
	require.Equal(t, "run is finished", again.NotExpiredReason)
	require.EqualValues(t, 2, again.Run.Version)
	require.Equal(t, "no waiting execution old enough", expire("exp-auto-mixed").NotExpiredReason)
	require.Empty(t, list(200))

	runsAfter, jobsAfter := countRows()
	require.Equal(t, runsBefore, runsAfter, "nothing is deleted")
	require.Equal(t, jobsBefore, jobsAfter, "nothing is deleted")
}
