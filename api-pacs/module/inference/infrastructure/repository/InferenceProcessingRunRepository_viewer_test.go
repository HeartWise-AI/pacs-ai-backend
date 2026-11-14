package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func viewerRunPlan() types.CreateInferenceIngestionProcessingRunPlan {
	userID := "user-a"
	return types.CreateInferenceIngestionProcessingRunPlan{
		Run: types.CreateInferenceIngestionProcessingRun{
			ID: "run-2", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger:        entity.InferenceIngestionProcessingRunTriggerViewerRequest,
			Phase:             entity.InferenceIngestionProcessingRunPhaseQueued,
			RequestedByUserID: &userID,
		},
		Executions: []types.CreateInferenceIngestionProcessingExecution{{
			ID: "execution-1", CandidateID: "candidate-1", ModelName: "EchoModel",
		}},
	}
}

func TestCreateProcessingRunPlanViewerRunIgnoresOtherActiveRunsButChecksModel(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("8:tenant-a1.2.3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM ingestion_processing_jobs jobs(.|\\n)*runs.tenant_id = \\$1(.|\\n)*jobs.tenant_id = \\$1(.|\\n)*runs.phase <> 'TERMINAL'(.|\\n)*jobs.model_name = ANY\\(\\$3\\)(.|\\n)*'pending', 'queued', 'running'(.|\\n)*AND NOT \\(jobs.status = 'pending' AND jobs.study_service_job_id IS NULL(.|\\n)*EXPECTED_JOB_MISSING").
		WithArgs("tenant-a", "1.2.3", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(run_number\\), 0\\) \\+ 1").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(sqlmock.NewRows([]string{"run_number"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").
		WithArgs("run-2", "tenant-a", "1.2.3", 2, entity.InferenceIngestionProcessingRunTriggerViewerRequest, entity.InferenceIngestionProcessingRunPhaseQueued, sqlmock.AnyArg()).
		WillReturnRows(processingRunRows(now))
	mock.ExpectQuery("INSERT INTO ingestion_processing_jobs").
		WillReturnRows(processingExecutionRows(now))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	result, err := repository.CreateProcessingRunPlan(context.Background(), viewerRunPlan())

	require.NoError(t, err)
	require.True(t, result.Created)
	require.Len(t, result.Executions, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunPlanViewerRunConflictsWithActiveSameModelExecution(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM ingestion_processing_jobs").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	_, err = repository.CreateProcessingRunPlan(context.Background(), viewerRunPlan())

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunPlanAutomaticReuseExcludesViewerRuns(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs(.|\\n)*CAST\\(run_trigger AS text\\) <> 'VIEWER_REQUEST'").
		WillReturnRows(emptyProcessingRunRows())
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"run_number"}).AddRow(3))
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").WillReturnRows(processingRunRows(time.Now()))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	_, err = repository.CreateProcessingRunPlan(context.Background(), types.CreateInferenceIngestionProcessingRunPlan{
		Run: types.CreateInferenceIngestionProcessingRun{
			ID: "run-3", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
		},
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunPlanManualStillConflictsWithAnyActiveRun(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WillReturnRows(processingRunRows(time.Now()))
	mock.ExpectRollback()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	_, err = repository.CreateProcessingRunPlan(context.Background(), types.CreateInferenceIngestionProcessingRunPlan{
		Run: types.CreateInferenceIngestionProcessingRun{
			ID: "run-3", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
		},
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSelectActiveProcessingRunExcludesViewerRuns(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(query string, _ interface{}, target interface{}) error {
		require.Contains(t, query, "CAST(run_trigger AS text) <> 'VIEWER_REQUEST'")
		*target.(*entity.InferenceIngestionProcessingRun) = entity.InferenceIngestionProcessingRun{ID: "run-1"}
		return nil
	}
	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	_, err := repository.SelectActiveProcessingRun(context.Background(), "tenant-a", "1.2.3")
	require.NoError(t, err)
}

// sqlx rewrites "::" to ":" in named queries. A "::text" cast in this filter
// made every active-run lookup fail and stopped automatic study processing.
func TestViewerRunExclusionSurvivesNamedQueryCompilation(t *testing.T) {
	compiled, _, err := sqlx.Named(
		"SELECT 1 WHERE tenant_id = :tenant_id "+viewerRunExclusion,
		map[string]interface{}{"tenant_id": "tenant-a"},
	)
	require.NoError(t, err)
	require.Contains(t, compiled, "CAST(run_trigger AS text) <> 'VIEWER_REQUEST'")
	require.NotContains(t, compiled, ":")
}
