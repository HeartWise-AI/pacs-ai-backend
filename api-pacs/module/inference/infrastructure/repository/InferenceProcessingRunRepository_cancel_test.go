package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func cancelTransition() types.ApplyInferenceIngestionProcessingTransition {
	message := "Cancelled by user"
	return types.ApplyInferenceIngestionProcessingTransition{
		TenantID: "tenant-a", ProcessingRunID: "run-2", ExecutionID: "execution-1",
		CandidateID: "candidate-1", ModelName: "EchoModel",
		Status:       entity.InferenceIngestionProcessingJobStatusCancelled,
		ErrorMessage: &message, OnlyIfCancellableBeforeCompute: true,
	}
}

func TestCancelTransitionLeavesAnExecutionHandedToStudyServiceUnderLock(t *testing.T) {
	for _, status := range []entity.InferenceIngestionProcessingJobStatus{
		entity.InferenceIngestionProcessingJobStatusQueued,
		entity.InferenceIngestionProcessingJobStatusRunning,
	} {
		t.Run(string(status), func(t *testing.T) {
			database, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { database.Close() })
			now := time.Now().UTC()

			mock.ExpectBegin()
			mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
				WillReturnRows(processingRunStateRows(now, entity.InferenceIngestionProcessingRunPhaseProcessing, 2, &now, nil))
			// The fixture row carries study_service_job_id "python-job-1".
			mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
				WillReturnRows(processingExecutionStateRows(now, status, nil, nil, nil))
			mock.ExpectRollback()

			repository := InferenceProcessingRunRepository{
				PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
			}
			result, err := repository.ApplyProcessingRunExecutionTransition(context.Background(), cancelTransition())

			require.NoError(t, err)
			require.Equal(t, "ignored", result.Outcome)
			require.False(t, result.Changed)
			require.Equal(t, status, result.Execution.Status)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCancelTransitionCancelsAPendingExecutionAndAggregatesTheRun(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC()
	cancelledRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "processing_run_id", "candidate_id", "tenant_id", "model_name", "status", "completed_at"}).
			AddRow("execution-1", "run-2", "candidate-1", "tenant-a", "EchoModel", "cancelled", now)
	}
	cancelledOutcome := entity.InferenceIngestionProcessingRunOutcomeCancelled

	mock.ExpectBegin()
	// The run was marked cancelled by a user before the transition.
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "phase", "version", "cancelled_by_user_id", "cancelled_at", "attention_reasons"}).
			AddRow("run-2", "tenant-a", "QUEUED", int64(1), "user-a", now, []byte("[]")))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WillReturnRows(processingExecutionRows(now))
	mock.ExpectQuery("UPDATE ingestion_processing_jobs").
		WithArgs(
			entity.InferenceIngestionProcessingJobStatusCancelled,
			nil, nil, nil, "Cancelled by user", nil, nil, nil, nil, nil, now, "execution-1",
		).
		WillReturnRows(cancelledRows())
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WithArgs("run-2").
		WillReturnRows(cancelledRows())
	mock.ExpectQuery("UPDATE ingestion_processing_runs").
		WithArgs(
			entity.InferenceIngestionProcessingRunPhaseTerminal,
			&cancelledOutcome,
			false,
			sqlmock.AnyArg(),
			nil,
			now,
			"run-2",
			"tenant-a",
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "phase", "outcome", "version", "cancelled_at"}).
			AddRow("run-2", "tenant-a", "TERMINAL", "CANCELLED", int64(2), now))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	transition := cancelTransition()
	transition.CompletedAt = &now
	result, err := repository.ApplyProcessingRunExecutionTransition(context.Background(), transition)

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.True(t, result.Changed)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, result.Run.Phase)
	require.Equal(t, cancelledOutcome, *result.Run.Outcome)
	require.Equal(t, 1, result.Counts.Cancelled)
	require.NotNil(t, result.Run.CancelledAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMarkProcessingRunCancelledKeepsTheFirstCancellationOfAnActiveTenantRun(t *testing.T) {
	now := time.Now().UTC()
	var capturedQuery string
	var capturedData types.MarkInferenceIngestionProcessingRunCancelled
	handler := &processingRunTestHandler{queryRow: func(query string, model interface{}, target interface{}) error {
		capturedQuery = query
		capturedData = model.(types.MarkInferenceIngestionProcessingRunCancelled)
		run := target.(*entity.InferenceIngestionProcessingRun)
		run.ID, run.CancelledAt = "run-1", &now
		return nil
	}}
	repository := &InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	run, err := repository.MarkProcessingRunCancelled(context.Background(), types.MarkInferenceIngestionProcessingRunCancelled{
		ID: "run-1", TenantID: "tenant-a", CancelledByUserID: "user-a", CancelledAt: now,
	})

	require.NoError(t, err)
	require.Equal(t, "run-1", run.ID)
	require.Equal(t, "user-a", capturedData.CancelledByUserID)
	require.Contains(t, capturedQuery, "cancelled_by_user_id = COALESCE(cancelled_by_user_id, :cancelled_by_user_id)")
	require.Contains(t, capturedQuery, "cancelled_at = COALESCE(cancelled_at, :cancelled_at)")
	require.Contains(t, capturedQuery, "WHERE id = :id AND tenant_id = :tenant_id AND phase <> 'TERMINAL'")
	// The aggregate columns belong to the execution transition.
	require.NotContains(t, capturedQuery, "outcome")
	require.NotContains(t, capturedQuery, "version")

	handler.queryRow = func(string, interface{}, interface{}) error { return sql.ErrNoRows }
	_, err = repository.MarkProcessingRunCancelled(context.Background(), types.MarkInferenceIngestionProcessingRunCancelled{
		ID: "run-1", TenantID: "tenant-b", CancelledByUserID: "user-b", CancelledAt: now,
	})
	require.EqualError(t, err, apiError.MissingRecord)
}

func TestSelectLatestStudyWideProcessingRunSkipsViewerRuns(t *testing.T) {
	var capturedQuery string
	var capturedData map[string]interface{}
	handler := &processingRunTestHandler{queryRow: func(query string, model interface{}, _ interface{}) error {
		capturedQuery = query
		capturedData = model.(map[string]interface{})
		return nil
	}}
	repository := &InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}

	_, err := repository.SelectLatestStudyWideProcessingRun(context.Background(), "tenant-a", "1.2.3")

	require.NoError(t, err)
	require.Equal(t, map[string]interface{}{"tenant_id": "tenant-a", "study_instance_uid": "1.2.3"}, capturedData)
	require.Contains(t, capturedQuery, "tenant_id = :tenant_id AND study_instance_uid = :study_instance_uid")
	require.Contains(t, capturedQuery, "CAST(run_trigger AS text) <> 'VIEWER_REQUEST'")
	require.Contains(t, capturedQuery, "ORDER BY run_number DESC LIMIT 1")
	require.NotContains(t, capturedQuery, "phase")
}
