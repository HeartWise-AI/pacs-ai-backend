package repository

import (
	"context"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// TestPredictionsRepositoryAgainstPostgreSQL runs the AI Predictions queries
// on a migrated database inside a rolled-back transaction. All identifiers
// are synthetic.
func TestPredictionsRepositoryAgainstPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("WORKLIST_INTEGRATION_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("WORKLIST_INTEGRATION_DATABASE_URL is not set")
	}

	database, err := sqlx.Connect("pgx", databaseURL)
	require.NoError(t, err)
	defer database.Close()
	tx, err := database.Beginx()
	require.NoError(t, err)
	defer tx.Rollback()
	repository := InferencePredictionsRepository{PostgresSQLDBHandlerInterface: &worklistIntegrationTxHandler{tx: tx}}
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, job := range []struct{ id, tenantID string }{
		{"aip-job-a", "aip-tenant-a"}, {"aip-job-b", "aip-tenant-b"},
	} {
		_, err = tx.Exec(`
			INSERT INTO ingestion_jobs (
				id, tenant_id, dicom_modality, container_id, model_id, model_name,
				model_version, modalities, stability_minutes, recent_window_minutes,
				missing_polls_threshold, schedule_start_timestamp,
				schedule_end_timestamp, status, created_at, updated_at
			) VALUES ($1, $2, 'XA', 'container', 'model', 'model', 'v1',
				ARRAY['XA'], 1, 60, 3, $3, $4, 'RUNNING', $3, $3)
		`, job.id, job.tenantID, now, now.Add(time.Hour))
		require.NoError(t, err)
	}

	type candidate struct {
		id, tenantID, jobID, study, status string
		patientID, accession, modalities   interface{}
	}
	for _, fixture := range []candidate{
		{"aip-cand-1", "aip-tenant-a", "aip-job-a", "9.1", "RETRIEVED", "MRN-001", "ACC-1", "XA"},
		{"aip-cand-2", "aip-tenant-a", "aip-job-a", "9.2", "GROWING", "MRN-002", "ACC-2", "US"},
		{"aip-cand-3", "aip-tenant-a", "aip-job-a", "9.3", "RETRIEVED", nil, nil, nil},
		{"aip-cand-b", "aip-tenant-b", "aip-job-b", "9.1", "RETRIEVED", "MRN-B", "ACC-B", "XA"},
	} {
		_, err = tx.Exec(`
			INSERT INTO ingestion_candidates (
				id, tenant_id, ingestion_job_id, study_instance_uid, patient_id, accession_number,
				modalities_in_study, first_seen_at, last_seen_at, last_changed_at, missing_polls,
				status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8, 0, $9, $8, $8)
		`, fixture.id, fixture.tenantID, fixture.jobID, fixture.study, fixture.patientID, fixture.accession,
			fixture.modalities, now, fixture.status)
		require.NoError(t, err)
	}

	type run struct {
		id, tenantID, study string
		number              int
		trigger, phase      string
		requestedBy         interface{}
		at                  time.Time
	}
	for _, fixture := range []run{
		{"aip-run-a1", "aip-tenant-a", "9.1", 1, "AUTO", "TERMINAL", nil, now.Add(-3 * time.Hour)},
		{"aip-run-a2", "aip-tenant-a", "9.1", 2, "VIEWER_REQUEST", "TERMINAL", "aip-user-1", now.Add(-time.Hour)},
		{"aip-run-c1", "aip-tenant-a", "9.2", 1, "AUTO", "PROCESSING", nil, now.Add(-10 * time.Minute)},
		{"aip-run-d1", "aip-tenant-a", "9.3", 1, "VIEWER_REQUEST", "TERMINAL", "aip-user-2", now.Add(-48 * time.Hour)},
		{"aip-run-b1", "aip-tenant-b", "9.1", 1, "AUTO", "TERMINAL", nil, now},
	} {
		_, err = tx.Exec(`
			INSERT INTO ingestion_processing_runs (
				id, tenant_id, study_instance_uid, run_number, run_trigger, phase,
				requested_by_user_id, attention_required, attention_reasons, version,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE, '[]', 1, $8, $8)
		`, fixture.id, fixture.tenantID, fixture.study, fixture.number, fixture.trigger, fixture.phase,
			fixture.requestedBy, fixture.at)
		require.NoError(t, err)
	}

	type execution struct {
		id, runID, candidateID, tenantID, model, status string
		at                                              time.Time
	}
	for _, fixture := range []execution{
		{"aip-ex-1", "aip-run-a1", "aip-cand-1", "aip-tenant-a", "DeepCORO-JEPA", "completed", now.Add(-3 * time.Hour)},
		{"aip-ex-2", "aip-run-a1", "aip-cand-1", "aip-tenant-a", "DeepCORO-SYNTAX", "failed", now.Add(-3 * time.Hour)},
		{"aip-ex-3", "aip-run-a2", "aip-cand-1", "aip-tenant-a", "DeepCORO-SYNTAX", "completed", now.Add(-time.Hour)},
		{"aip-ex-4", "aip-run-c1", "aip-cand-2", "aip-tenant-a", "PanEcho", "pending", now.Add(-10 * time.Minute)},
		{"aip-ex-5", "aip-run-c1", "aip-cand-2", "aip-tenant-a", "EchoPrime", "running", now.Add(-10 * time.Minute)},
		{"aip-ex-6", "aip-run-d1", "aip-cand-3", "aip-tenant-a", "DeepCORO-JEPA", "skipped", now.Add(-48 * time.Hour)},
		{"aip-ex-b", "aip-run-b1", "aip-cand-b", "aip-tenant-b", "DeepCORO-JEPA", "completed", now},
	} {
		_, err = tx.Exec(`
			INSERT INTO ingestion_processing_jobs (
				id, processing_run_id, candidate_id, tenant_id, model_name, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		`, fixture.id, fixture.runID, fixture.candidateID, fixture.tenantID, fixture.model, fixture.status, fixture.at)
		require.NoError(t, err)
	}

	exams := func(data types.ListPredictionExams) ([]string, int) {
		t.Helper()
		if data.TenantID == "" {
			data.TenantID = "aip-tenant-a"
		}
		if data.Scope == "" {
			data.Scope = types.PredictionsScopeAll
		}
		if data.Limit == 0 {
			data.Limit = 10
		}
		page, err := repository.ListPredictionExams(ctx, data)
		require.NoError(t, err)
		uids := []string{}
		for _, exam := range page.Exams {
			uids = append(uids, exam.StudyInstanceUID)
		}
		return uids, page.Total
	}

	uids, total := exams(types.ListPredictionExams{})
	require.Equal(t, []string{"9.2", "9.1", "9.3"}, uids, "newest activity first, other tenant excluded")
	require.Equal(t, 3, total)

	page, err := repository.ListPredictionExams(ctx, types.ListPredictionExams{
		TenantID: "aip-tenant-a", Scope: types.PredictionsScopeAll, Limit: 10,
	})
	require.NoError(t, err)
	require.Equal(t, "MRN-001", *page.Exams[1].PatientID)
	require.Equal(t, "ACC-1", *page.Exams[1].AccessionNumber)
	require.Nil(t, page.Exams[2].PatientID, "viewer-only exams have no stored metadata")

	uids, _ = exams(types.ListPredictionExams{Scope: types.PredictionsScopeRequestedBy, RequestedByUserID: "aip-user-1"})
	require.Equal(t, []string{"9.1"}, uids)
	uids, _ = exams(types.ListPredictionExams{Scope: types.PredictionsScopeAutomatic})
	require.Equal(t, []string{"9.2", "9.1"}, uids)
	uids, _ = exams(types.ListPredictionExams{Scope: types.PredictionsScopePeople})
	require.Equal(t, []string{"9.1", "9.3"}, uids)

	// The status filter reads the latest execution of each model: the failed
	// SYNTAX of 9.1 was rerun and completed.
	uids, _ = exams(types.ListPredictionExams{Status: types.PredictionsExamStatusAttention})
	require.Equal(t, []string{"9.3"}, uids)
	uids, _ = exams(types.ListPredictionExams{Status: types.PredictionsExamStatusCompleted})
	require.Equal(t, []string{"9.1"}, uids)
	uids, _ = exams(types.ListPredictionExams{Status: types.PredictionsExamStatusActive})
	require.Equal(t, []string{"9.2"}, uids)

	uids, _ = exams(types.ListPredictionExams{ModelName: "DeepCORO-SYNTAX"})
	require.Equal(t, []string{"9.1"}, uids)
	uids, _ = exams(types.ListPredictionExams{Modality: "us"})
	require.Equal(t, []string{"9.2"}, uids)
	uids, _ = exams(types.ListPredictionExams{Search: "acc-1"})
	require.Equal(t, []string{"9.1"}, uids)
	uids, _ = exams(types.ListPredictionExams{Search: "MRN-00"})
	require.Equal(t, []string{"9.2", "9.1"}, uids)
	uids, _ = exams(types.ListPredictionExams{Search: "%"})
	require.Empty(t, uids, "LIKE wildcards in the search are literal")
	uids, _ = exams(types.ListPredictionExams{Search: "9.3"})
	require.Equal(t, []string{"9.3"}, uids)

	from := now.Add(-5 * time.Hour)
	uids, _ = exams(types.ListPredictionExams{From: &from})
	require.Equal(t, []string{"9.2", "9.1"}, uids)
	to := now.Add(-30 * time.Minute)
	uids, _ = exams(types.ListPredictionExams{From: &from, To: &to})
	require.Equal(t, []string{"9.1"}, uids)

	uids, total = exams(types.ListPredictionExams{Limit: 1, Offset: 1})
	require.Equal(t, []string{"9.1"}, uids)
	require.Equal(t, 3, total)

	latest, err := repository.ListPredictionExecutions(ctx, types.ListPredictionExecutions{
		TenantID: "aip-tenant-a", StudyInstanceUIDs: []string{"9.1", "9.2"}, LatestOnly: true,
	})
	require.NoError(t, err)
	require.Len(t, latest, 4)
	byModel := map[string]types.PredictionExecutionRow{}
	for _, row := range latest {
		byModel[row.StudyInstanceUID+"/"+row.ModelName] = row
	}
	syntax := byModel["9.1/DeepCORO-SYNTAX"]
	require.Equal(t, "aip-ex-3", syntax.ExecutionID)
	require.Equal(t, 1, syntax.EarlierRuns)
	require.Equal(t, "aip-user-1", *syntax.RequestedByUserID)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerViewerRequest, syntax.RunTrigger)
	require.Equal(t, 0, byModel["9.1/DeepCORO-JEPA"].EarlierRuns)
	panEcho := byModel["9.2/PanEcho"]
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusPending, panEcho.Status)
	require.Equal(t, entity.InferenceIngestionCandidateStatusGrowing, *panEcho.CandidateStatus)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseProcessing, panEcho.RunPhase)

	automaticLines, err := repository.ListPredictionExecutions(ctx, types.ListPredictionExecutions{
		TenantID: "aip-tenant-a", StudyInstanceUIDs: []string{"9.1"}, LaunchedBy: types.PredictionsScopeAutomatic,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"aip-ex-1", "aip-ex-2"}, []string{automaticLines[0].ExecutionID, automaticLines[1].ExecutionID})
	allLines, err := repository.ListPredictionExecutions(ctx, types.ListPredictionExecutions{
		TenantID: "aip-tenant-a", StudyInstanceUIDs: []string{"9.1"},
	})
	require.NoError(t, err)
	require.Equal(t, "aip-ex-3", allLines[0].ExecutionID, "newest run first")
	require.Len(t, allLines, 3)

	history, err := repository.ListPredictionModelHistory(ctx, types.ListPredictionModelHistory{
		TenantID: "aip-tenant-a", StudyInstanceUID: "9.1", ModelName: "DeepCORO-SYNTAX", Limit: 1,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"aip-ex-3", "aip-ex-2"}, []string{history[0].ExecutionID, history[1].ExecutionID},
		"limit+1 rows, newest first")

	launchers, err := repository.ListPredictionLaunchers(ctx, "aip-tenant-a", []string{"9.1"})
	require.NoError(t, err)
	require.Len(t, launchers, 2)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerAuto, launchers[0].RunTrigger)
	require.Equal(t, "aip-user-1", *launchers[1].RequestedByUserID)

	// Tenant isolation: the same Study Instance UID in another tenant.
	other, err := repository.ListPredictionExecutions(ctx, types.ListPredictionExecutions{
		TenantID: "aip-tenant-b", StudyInstanceUIDs: []string{"9.1"}, LatestOnly: true,
	})
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.Equal(t, "aip-ex-b", other[0].ExecutionID)
	otherHistory, err := repository.ListPredictionModelHistory(ctx, types.ListPredictionModelHistory{
		TenantID: "aip-tenant-b", StudyInstanceUID: "9.1", ModelName: "DeepCORO-SYNTAX", Limit: 5,
	})
	require.NoError(t, err)
	require.Empty(t, otherHistory)
	uids, _ = exams(types.ListPredictionExams{TenantID: "aip-tenant-b", Search: "MRN-001"})
	require.Empty(t, uids)

	// Executions that never ran: pending or queued without a study-service
	// job long after their run was created are stalled, need attention and
	// are not active. A separate tenant keeps the assertions above unchanged.
	_, err = tx.Exec(`
		INSERT INTO ingestion_jobs (
			id, tenant_id, dicom_modality, container_id, model_id, model_name,
			model_version, modalities, stability_minutes, recent_window_minutes,
			missing_polls_threshold, schedule_start_timestamp,
			schedule_end_timestamp, status, created_at, updated_at
		) VALUES ('aip-job-c', 'aip-tenant-c', 'XA', 'container', 'model', 'model', 'v1',
			ARRAY['XA'], 1, 60, 3, $1, $2, 'RUNNING', $1, $1)
	`, now, now.Add(time.Hour))
	require.NoError(t, err)
	stalledBefore := now.Add(-24 * time.Hour)
	for _, fixture := range []struct {
		study, status     string
		studyServiceJobID interface{}
		skipReasonCode    interface{}
		at                time.Time
	}{
		{"8.1", "pending", nil, nil, now.Add(-30 * time.Hour)},                 // stalled
		{"8.2", "queued", "ss-8-2", nil, now.Add(-31 * time.Hour)},             // at study-service: active
		{"8.3", "queued", nil, nil, now.Add(-23 * time.Hour)},                  // recent: active
		{"8.4", "queued", "  ", nil, now.Add(-32 * time.Hour)},                 // blank job id: stalled
		{"8.5", "completed", nil, nil, now.Add(-33 * time.Hour)},               // finished
		{"8.6", "skipped", nil, "EXPIRED_NEVER_RAN", now.Add(-34 * time.Hour)}, // expired
	} {
		suffix := fixture.study
		_, err = tx.Exec(`
			INSERT INTO ingestion_candidates (
				id, tenant_id, ingestion_job_id, study_instance_uid, first_seen_at, last_seen_at,
				last_changed_at, missing_polls, status, created_at, updated_at
			) VALUES ($1, 'aip-tenant-c', 'aip-job-c', $2, $3, $3, $3, 0, 'RETRIEVED', $3, $3)
		`, "aip-cand-c"+suffix, fixture.study, fixture.at)
		require.NoError(t, err)
		_, err = tx.Exec(`
			INSERT INTO ingestion_processing_runs (
				id, tenant_id, study_instance_uid, run_number, run_trigger, phase,
				attention_required, attention_reasons, version, created_at, updated_at
			) VALUES ($1, 'aip-tenant-c', $2, 1, 'AUTO', 'QUEUED', FALSE, '[]', 1, $3, $3)
		`, "aip-run-c"+suffix, fixture.study, fixture.at)
		require.NoError(t, err)
		_, err = tx.Exec(`
			INSERT INTO ingestion_processing_jobs (
				id, processing_run_id, candidate_id, tenant_id, model_name, status,
				study_service_job_id, skip_reason_code, created_at, updated_at
			) VALUES ($1, $2, $3, 'aip-tenant-c', 'DeepCORO-JEPA', $4, $5, $6, $7, $7)
		`, "aip-ex-c"+suffix, "aip-run-c"+suffix, "aip-cand-c"+suffix, fixture.status, fixture.studyServiceJobID,
			fixture.skipReasonCode, fixture.at)
		require.NoError(t, err)
	}
	uids, total = exams(types.ListPredictionExams{TenantID: "aip-tenant-c", StalledBefore: stalledBefore})
	require.Equal(t, []string{"8.3", "8.1", "8.2", "8.4", "8.5", "8.6"}, uids)
	require.Equal(t, 6, total)
	uids, _ = exams(types.ListPredictionExams{TenantID: "aip-tenant-c", StalledBefore: stalledBefore,
		Status: types.PredictionsExamStatusActive})
	require.Equal(t, []string{"8.3", "8.2"}, uids, "stalled exams are not active")
	uids, _ = exams(types.ListPredictionExams{TenantID: "aip-tenant-c", StalledBefore: stalledBefore,
		Status: types.PredictionsExamStatusAttention})
	require.Equal(t, []string{"8.1", "8.4"}, uids, "stalled exams need attention, expired ones do not")
	uids, _ = exams(types.ListPredictionExams{TenantID: "aip-tenant-c", StalledBefore: stalledBefore,
		Status: types.PredictionsExamStatusCompleted})
	require.Equal(t, []string{"8.5"}, uids)
	// Without a limit nothing is stalled (as before this state existed).
	uids, _ = exams(types.ListPredictionExams{TenantID: "aip-tenant-c", Status: types.PredictionsExamStatusActive})
	require.Equal(t, []string{"8.3", "8.1", "8.2", "8.4"}, uids)

	stalledRows, err := repository.ListPredictionExecutions(ctx, types.ListPredictionExecutions{
		TenantID: "aip-tenant-c", StudyInstanceUIDs: []string{"8.1"}, LatestOnly: true,
	})
	require.NoError(t, err)
	require.Len(t, stalledRows, 1)
	require.Nil(t, stalledRows[0].StudyServiceJobID)
	require.True(t, stalledRows[0].RunCreatedAt.Before(stalledBefore))

	// Activity is a real event. Reconciliation or a restart touching runs and
	// executions (updated_at) moves nothing.
	_, err = tx.Exec(`UPDATE ingestion_processing_runs SET version = version + 1, updated_at = $2 WHERE tenant_id = $1`,
		"aip-tenant-a", now.Add(time.Minute))
	require.NoError(t, err)
	_, err = tx.Exec(`UPDATE ingestion_processing_jobs SET last_event_id = last_event_id, updated_at = $2 WHERE tenant_id = $1`,
		"aip-tenant-a", now.Add(time.Minute))
	require.NoError(t, err)
	page, err = repository.ListPredictionExams(ctx, types.ListPredictionExams{
		TenantID: "aip-tenant-a", Scope: types.PredictionsScopeAll, Limit: 10,
	})
	require.NoError(t, err)
	require.Equal(t, "9.3", page.Exams[2].StudyInstanceUID)
	require.WithinDuration(t, now.Add(-48*time.Hour), page.Exams[2].LastActivityAt, time.Second,
		"the run creation, not the touch")
	uids, _ = exams(types.ListPredictionExams{From: &from})
	require.Equal(t, []string{"9.2", "9.1"}, uids, "a touched old exam is not in a recent range")

	// A completion is activity: it moves the exam up and into the range,
	// although its run was created long before.
	_, err = tx.Exec(`UPDATE ingestion_processing_jobs SET started_at = $2, completed_at = $3 WHERE id = $1`,
		"aip-ex-6", now.Add(-3*time.Minute), now.Add(-2*time.Minute))
	require.NoError(t, err)
	uids, _ = exams(types.ListPredictionExams{From: &from})
	require.Equal(t, []string{"9.3", "9.2", "9.1"}, uids)
	to = now.Add(-5 * time.Minute)
	uids, _ = exams(types.ListPredictionExams{From: &from, To: &to})
	require.Equal(t, []string{"9.2", "9.1"}, uids, "the completion two minutes ago is after the end")
	started := now.Add(-4 * time.Hour)
	_, err = tx.Exec(`UPDATE ingestion_processing_jobs SET started_at = $2, completed_at = NULL WHERE id = $1`, "aip-ex-6", started)
	require.NoError(t, err)
	page, err = repository.ListPredictionExams(ctx, types.ListPredictionExams{
		TenantID: "aip-tenant-a", Scope: types.PredictionsScopeAll, Limit: 10,
	})
	require.NoError(t, err)
	require.Equal(t, "9.3", page.Exams[2].StudyInstanceUID)
	require.WithinDuration(t, started, page.Exams[2].LastActivityAt, time.Second, "started when not completed")
}
