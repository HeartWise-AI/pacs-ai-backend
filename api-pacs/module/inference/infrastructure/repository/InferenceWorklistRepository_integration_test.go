package repository

import (
	"context"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

type worklistIntegrationTxHandler struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
	tx *sqlx.Tx
}

func (handler *worklistIntegrationTxHandler) Query(statement string, model interface{}, target interface{}) error {
	prepared, err := handler.tx.PrepareNamed(statement)
	if err != nil {
		return err
	}
	defer prepared.Close()
	return prepared.Select(target, model)
}

func (handler *worklistIntegrationTxHandler) QueryRow(statement string, model interface{}, target interface{}) error {
	prepared, err := handler.tx.PrepareNamed(statement)
	if err != nil {
		return err
	}
	defer prepared.Close()
	return prepared.Get(target, model)
}

func TestWorklistRepositoriesAgainstPostgreSQL(t *testing.T) {
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
	handler := &worklistIntegrationTxHandler{tx: tx}

	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, fixture := range []struct {
		id       string
		tenantID string
	}{
		{id: "rtw-int-job-a", tenantID: "rtw-int-tenant-a"},
		{id: "rtw-int-job-b", tenantID: "rtw-int-tenant-b"},
	} {
		_, err = tx.Exec(`
			INSERT INTO ingestion_jobs (
				id, tenant_id, dicom_modality, container_id, model_id, model_name,
				model_version, modalities, stability_minutes, recent_window_minutes,
				missing_polls_threshold, schedule_start_timestamp,
				schedule_end_timestamp, status, created_at, updated_at
			) VALUES ($1, $2, 'US', 'container', 'model', 'model', 'v1',
				ARRAY['US'], 1, 60, 3, $3, $4, 'RUNNING', $3, $3)
		`, fixture.id, fixture.tenantID, now, now.Add(time.Hour))
		require.NoError(t, err)
	}

	candidateFixtures := []struct {
		id, tenantID, jobID, studyUID, status, retrievalState string
	}{
		{"rtw-int-candidate-retrieval", "rtw-int-tenant-a", "rtw-int-job-a", "rtw-int-study-retrieval", "RETRIEVAL_QUEUED", "RUNNING"},
		{"rtw-int-candidate-active", "rtw-int-tenant-a", "rtw-int-job-a", "rtw-int-study-active", "RETRIEVED", "SUCCESS"},
		{"rtw-int-candidate-other", "rtw-int-tenant-b", "rtw-int-job-b", "rtw-int-study-active", "RETRIEVED", "SUCCESS"},
	}
	for _, fixture := range candidateFixtures {
		_, err = tx.Exec(`
			INSERT INTO ingestion_candidates (
				id, tenant_id, ingestion_job_id, study_instance_uid,
				first_seen_at, last_seen_at, last_changed_at, missing_polls,
				status, last_retrieval_state, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $5, $5, 0, $6, $7, $5, $5)
		`, fixture.id, fixture.tenantID, fixture.jobID, fixture.studyUID, now, fixture.status, fixture.retrievalState)
		require.NoError(t, err)
	}

	_, err = tx.Exec(`
		INSERT INTO ingestion_processing_runs (
			id, tenant_id, study_instance_uid, run_number, run_trigger, phase,
			outcome, attention_required, attention_reasons, version,
			started_at, completed_at, created_at, updated_at
		) VALUES
			('rtw-int-run-a1', 'rtw-int-tenant-a', 'rtw-int-study-active', 1,
			 'AUTO', 'TERMINAL', 'SUCCESS', FALSE, '[]', 3, $1, $2, $1, $2),
			('rtw-int-run-a2', 'rtw-int-tenant-a', 'rtw-int-study-active', 2,
			 'MANUAL_REPROCESS', 'PROCESSING', NULL, TRUE,
			 '[{"code":"PROCESSING_STALE"}]', 5, $2, NULL, $2, $2),
			('rtw-int-run-b1', 'rtw-int-tenant-b', 'rtw-int-study-active', 1,
			 'AUTO', 'TERMINAL', 'FAILED', FALSE, '[]', 2, $1, $2, $1, $2)
	`, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)

	processingFixtures := []struct {
		id, runID, candidateID, tenantID, modelName, status string
	}{
		{"rtw-int-execution-completed", "rtw-int-run-a2", "rtw-int-candidate-active", "rtw-int-tenant-a", "completed-model", "completed"},
		{"rtw-int-execution-running", "rtw-int-run-a2", "rtw-int-candidate-active", "rtw-int-tenant-a", "running-model", "running"},
		{"rtw-int-execution-failed", "rtw-int-run-a2", "rtw-int-candidate-active", "rtw-int-tenant-a", "failed-model", "failed"},
		{"rtw-int-execution-old", "rtw-int-run-a1", "rtw-int-candidate-active", "rtw-int-tenant-a", "old-model", "completed"},
		{"rtw-int-execution-other", "rtw-int-run-b1", "rtw-int-candidate-other", "rtw-int-tenant-b", "other-model", "failed"},
	}
	for _, fixture := range processingFixtures {
		_, err = tx.Exec(`
			INSERT INTO ingestion_processing_jobs (
				id, processing_run_id, candidate_id, tenant_id, model_name,
				status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		`, fixture.id, fixture.runID, fixture.candidateID, fixture.tenantID, fixture.modelName, fixture.status, now)
		require.NoError(t, err)
	}

	queryRepository := InferenceQueryRepository{PostgresSQLDBHandlerInterface: handler}
	statusPage, err := queryRepository.ListWorklistStudyStatuses(types.ListWorklistStudyStatuses{
		TenantID:          "rtw-int-tenant-a",
		StudyInstanceUIDs: []string{"rtw-int-study-retrieval", "rtw-int-study-active"},
		Limit:             10,
	})
	require.NoError(t, err)
	require.False(t, statusPage.HasMore)
	require.Len(t, statusPage.Studies, 2)

	statuses := make(map[string]types.WorklistStudyStatus, len(statusPage.Studies))
	for _, status := range statusPage.Studies {
		statuses[status.StudyInstanceUID] = status
	}
	require.Nil(t, statuses["rtw-int-study-retrieval"].RunID)
	require.Equal(t, entity.InferenceIngestionCandidateStatusRetrievalQueued, statuses["rtw-int-study-retrieval"].IngestionStatus)
	active := statuses["rtw-int-study-active"]
	require.Equal(t, "rtw-int-run-a2", *active.RunID)
	require.Equal(t, 3, active.ExpectedModels)
	require.Equal(t, 1, active.CompletedModels)
	require.Equal(t, 1, active.RunningModels)
	require.Equal(t, 1, active.FailedModels)
	require.Equal(t, 1, active.ActiveModels)
	require.True(t, active.AttentionRequired)

	runRepository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	history, err := runRepository.ListProcessingRunHistoryPage(context.Background(), types.ListInferenceIngestionProcessingRuns{
		TenantID: "rtw-int-tenant-a", StudyInstanceUID: "rtw-int-study-active", Limit: 1,
	})
	require.NoError(t, err)
	require.True(t, history.HasMore)
	require.Equal(t, "rtw-int-run-a2", history.Runs[0].ID)

	executions, err := runRepository.ListProcessingRunExecutionsByRunIDs(context.Background(), types.ListInferenceIngestionProcessingRunExecutions{
		TenantID: "rtw-int-tenant-a", ProcessingRunIDs: []string{"rtw-int-run-a2"},
	})
	require.NoError(t, err)
	require.Len(t, executions, 3)
	for _, execution := range executions {
		require.Equal(t, "rtw-int-tenant-a", execution.TenantID)
		require.Equal(t, "rtw-int-run-a2", *execution.ProcessingRunID)
	}

	_, err = runRepository.SelectProcessingRun(context.Background(), "rtw-int-tenant-b", "rtw-int-run-a2")
	require.EqualError(t, err, apiError.MissingRecord)
}
