package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func legacyBackfillExecutionRows(now time.Time, modelNames ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{
		"id", "processing_run_id", "candidate_id", "tenant_id", "model_name", "model_version",
		"modality", "status", "study_service_job_id", "error_message", "skip_reason_code",
		"skip_reason_message", "last_event_id", "last_event_sequence", "started_at", "completed_at",
		"created_at", "updated_at",
	})
	for index, modelName := range modelNames {
		startedAt := now.Add(time.Duration(index) * time.Minute)
		completedAt := startedAt.Add(time.Minute)
		rows.AddRow(
			fmt.Sprintf("execution-%d", index+1), nil, fmt.Sprintf("candidate-%d", index+1),
			"tenant-a", modelName, "v1", "US", "completed", fmt.Sprintf("python-%d", index+1),
			nil, nil, nil, nil, nil, startedAt, completedAt, now.Add(time.Duration(index)*time.Minute), completedAt,
		)
	}
	return rows
}

func legacyBackfillRunRows(now time.Time) *sqlmock.Rows {
	return emptyProcessingRunRows().AddRow(
		"legacy-run-1", "tenant-a", "1.2.3", 1, "LEGACY_IMPORT", "TERMINAL",
		"SUCCESS", false, []byte("[]"), int64(1), now, now.Add(2*time.Minute),
		now, now.Add(2*time.Minute),
	)
}

func newLegacyBackfillSQLMock(t *testing.T) (*InferenceProcessingRunRepository, sqlmock.Sqlmock) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	return &InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}, mock
}

func expectLegacyBackfillLockedExecutions(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-a\x001.2.3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("SELECT jobs.\\*").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(rows)
}

func expectLegacyBackfillRunInsert(mock sqlmock.Sqlmock, now time.Time) {
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").
		WithArgs(
			"legacy-run-1", "tenant-a", "1.2.3",
			entity.InferenceIngestionProcessingRunTriggerLegacyImport,
			entity.InferenceIngestionProcessingRunPhaseTerminal,
			sqlmock.AnyArg(), false, sqlmock.AnyArg(), int64(1),
			now, now.Add(2*time.Minute), now, now.Add(2*time.Minute),
		).
		WillReturnRows(legacyBackfillRunRows(now))
}

func TestImportLegacyProcessingRunCommitsRunAndEveryExecutionAtomically(t *testing.T) {
	repository, mock := newLegacyBackfillSQLMock(t)
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	expectLegacyBackfillLockedExecutions(mock, legacyBackfillExecutionRows(now, "model-one", "model-two"))
	expectLegacyBackfillRunInsert(mock, now)
	mock.ExpectExec("UPDATE ingestion_processing_jobs").
		WithArgs("legacy-run-1", pq.Array([]string{"execution-1", "execution-2"})).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM ingestion_processing_jobs").
		WithArgs("legacy-run-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectCommit()

	result, err := repository.ImportLegacyProcessingRun(context.Background(), types.ImportLegacyProcessingRun{
		RunID: "legacy-run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerLegacyImport, result.Run.RunTrigger)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, result.Run.Phase)
	require.Equal(t, int64(1), result.Run.Version)
	require.Equal(t, 2, result.Counts.Expected)
	require.Equal(t, 2, result.Counts.Completed)
	require.Equal(t, 2, result.LinkedExecutions)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLegacyProcessingRunRejectsExistingRunAndRollsBack(t *testing.T) {
	repository, mock := newLegacyBackfillSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-a\x001.2.3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err := repository.ImportLegacyProcessingRun(context.Background(), types.ImportLegacyProcessingRun{
		RunID: "legacy-run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLegacyProcessingRunRejectsAmbiguousModelPlanAndRollsBack(t *testing.T) {
	repository, mock := newLegacyBackfillSQLMock(t)
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	expectLegacyBackfillLockedExecutions(mock, legacyBackfillExecutionRows(now, "Model-One", " model-one "))
	mock.ExpectRollback()

	_, err := repository.ImportLegacyProcessingRun(context.Background(), types.ImportLegacyProcessingRun{
		RunID: "legacy-run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.InvalidPayload)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLegacyProcessingRunRollsBackWhenLinkCountDoesNotMatchPlan(t *testing.T) {
	repository, mock := newLegacyBackfillSQLMock(t)
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	expectLegacyBackfillLockedExecutions(mock, legacyBackfillExecutionRows(now, "model-one", "model-two"))
	expectLegacyBackfillRunInsert(mock, now)
	mock.ExpectExec("UPDATE ingestion_processing_jobs").
		WithArgs("legacy-run-1", pq.Array([]string{"execution-1", "execution-2"})).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()

	_, err := repository.ImportLegacyProcessingRun(context.Background(), types.ImportLegacyProcessingRun{
		RunID: "legacy-run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLegacyProcessingRunRollsBackWhenPersistedCountCannotBeVerified(t *testing.T) {
	repository, mock := newLegacyBackfillSQLMock(t)
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	expectLegacyBackfillLockedExecutions(mock, legacyBackfillExecutionRows(now, "model-one", "model-two"))
	expectLegacyBackfillRunInsert(mock, now)
	mock.ExpectExec("UPDATE ingestion_processing_jobs").
		WithArgs("legacy-run-1", pq.Array([]string{"execution-1", "execution-2"})).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM ingestion_processing_jobs").
		WithArgs("legacy-run-1").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	_, err := repository.ImportLegacyProcessingRun(context.Background(), types.ImportLegacyProcessingRun{
		RunID: "legacy-run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListLegacyProcessingRunBackfillRowsUsesReadOnlyMinimalProjection(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		normalized := strings.ToLower(query)
		require.Contains(t, normalized, "where jobs.processing_run_id is null")
		require.Contains(t, normalized, "exists (")
		require.Contains(t, normalized, "join ingestion_candidates")
		require.Contains(t, normalized, "order by jobs.tenant_id, candidates.study_instance_uid")
		require.NotContains(t, normalized, "patient_id")
		require.NotContains(t, normalized, "accession_number")
		require.NotContains(t, normalized, "result_json")
		require.NotContains(t, normalized, "insert ")
		require.NotContains(t, normalized, "update ")
		require.Empty(t, model.(map[string]interface{}))

		rows := target.(*[]types.LegacyProcessingRunBackfillRow)
		*rows = append(*rows, types.LegacyProcessingRunBackfillRow{
			ExecutionID: "execution-1", CandidateID: "candidate-1",
			ExecutionTenantID: "tenant-a", CandidateTenantID: "tenant-a",
			StudyInstanceUID: "study-1", ModelName: "model-one",
			Status: entity.InferenceIngestionProcessingJobStatusCompleted,
		})
		return nil
	}
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	rows, err := repository.ListLegacyProcessingRunBackfillRows(context.Background())

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "execution-1", rows[0].ExecutionID)
}

func TestListLegacyProcessingRunBackfillRowsMapsDatabaseFailure(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(string, interface{}, interface{}) error { return errors.New("query failed") }
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	_, err := repository.ListLegacyProcessingRunBackfillRows(context.Background())

	require.EqualError(t, err, apiError.DatabaseError)
}
