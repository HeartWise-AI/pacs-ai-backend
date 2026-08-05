package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

type processingRunTestHandler struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
	db       *sqlx.DB
	query    func(string, interface{}, interface{}) error
	queryRow func(string, interface{}, interface{}) error
}

func (handler *processingRunTestHandler) Begin() (*sqlx.Tx, error) {
	return handler.db.Beginx()
}

func (handler *processingRunTestHandler) Query(query string, model interface{}, target interface{}) error {
	return handler.query(query, model, target)
}

func (handler *processingRunTestHandler) QueryRow(query string, model interface{}, target interface{}) error {
	return handler.queryRow(query, model, target)
}

func emptyProcessingRunRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "study_instance_uid", "run_number", "run_trigger", "phase",
		"outcome", "attention_required", "attention_reasons", "version", "started_at",
		"completed_at", "created_at", "updated_at",
	})
}

func processingRunRows(now time.Time) *sqlmock.Rows {
	return emptyProcessingRunRows().AddRow(
		"run-2", "tenant-a", "1.2.3", 2, "AUTO", "QUEUED",
		nil, false, []byte("[]"), int64(1), nil, nil, now, now,
	)
}

func processingExecutionRows(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "processing_run_id", "candidate_id", "tenant_id", "model_name", "model_version",
		"modality", "status", "study_service_job_id", "error_message", "skip_reason_code",
		"skip_reason_message", "last_event_id", "last_event_sequence", "started_at", "completed_at",
		"created_at", "updated_at",
	}).AddRow(
		"execution-1", "run-2", "candidate-1", "tenant-a", "EchoModel", "v1",
		"US", "pending", nil, nil, nil, nil, nil, nil, nil, nil, now, now,
	)
}

func processingRunStateRows(
	now time.Time,
	phase entity.InferenceIngestionProcessingRunPhase,
	version int64,
	startedAt *time.Time,
	completedAt *time.Time,
) *sqlmock.Rows {
	return emptyProcessingRunRows().AddRow(
		"run-2", "tenant-a", "1.2.3", 2, "AUTO", phase,
		nil, false, []byte("[]"), version, startedAt, completedAt, now, now,
	)
}

func processingExecutionStateRows(
	now time.Time,
	status entity.InferenceIngestionProcessingJobStatus,
	lastEventID *string,
	lastEventSequence *int64,
	startedAt *time.Time,
) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "processing_run_id", "candidate_id", "tenant_id", "model_name", "model_version",
		"modality", "status", "study_service_job_id", "error_message", "skip_reason_code",
		"skip_reason_message", "last_event_id", "last_event_sequence", "started_at", "completed_at",
		"created_at", "updated_at",
	}).AddRow(
		"execution-1", "run-2", "candidate-1", "tenant-a", "EchoModel", "v1",
		"US", status, "python-job-1", nil, nil, nil, lastEventID, lastEventSequence,
		startedAt, nil, now, now,
	)
}

func TestApplyProcessingRunExecutionTransitionUpdatesExecutionAndAggregateAtomically(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC()
	eventID := "event-running"
	eventSequence := int64(2)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WithArgs("run-2", "tenant-a").
		WillReturnRows(processingRunStateRows(now, entity.InferenceIngestionProcessingRunPhaseQueued, 1, nil, nil))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WithArgs("execution-1", "run-2", "candidate-1", "tenant-a", "EchoModel").
		WillReturnRows(processingExecutionStateRows(now, entity.InferenceIngestionProcessingJobStatusQueued, nil, nil, nil))
	mock.ExpectQuery("UPDATE ingestion_processing_jobs").
		WithArgs(
			entity.InferenceIngestionProcessingJobStatusRunning,
			nil, nil, nil, nil, nil, nil, eventID, eventSequence, now, nil, "execution-1",
		).
		WillReturnRows(processingExecutionStateRows(
			now, entity.InferenceIngestionProcessingJobStatusRunning, &eventID, &eventSequence, &now,
		))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WithArgs("run-2").
		WillReturnRows(processingExecutionStateRows(
			now, entity.InferenceIngestionProcessingJobStatusRunning, &eventID, &eventSequence, &now,
		))
	mock.ExpectQuery("UPDATE ingestion_processing_runs").
		WithArgs(
			entity.InferenceIngestionProcessingRunPhaseProcessing,
			nil,
			false,
			sqlmock.AnyArg(),
			now,
			nil,
			"run-2",
			"tenant-a",
		).
		WillReturnRows(processingRunStateRows(
			now, entity.InferenceIngestionProcessingRunPhaseProcessing, 2, &now, nil,
		))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	result, err := repository.ApplyProcessingRunExecutionTransition(
		context.Background(),
		types.ApplyInferenceIngestionProcessingTransition{
			TenantID: "tenant-a", ProcessingRunID: "run-2", ExecutionID: "execution-1",
			CandidateID: "candidate-1", ModelName: "EchoModel",
			Status:  entity.InferenceIngestionProcessingJobStatusRunning,
			EventID: &eventID, EventSequence: &eventSequence, StartedAt: &now,
		},
	)

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusRunning, result.Execution.Status)
	require.Equal(t, int64(2), result.Run.Version)
	require.Equal(t, 1, result.Counts.Running)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyProcessingRunExecutionTransitionRechecksDuplicateEventUnderLock(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC()
	eventID := "event-running"
	eventSequence := int64(2)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WillReturnRows(processingRunStateRows(now, entity.InferenceIngestionProcessingRunPhaseProcessing, 2, &now, nil))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WillReturnRows(processingExecutionStateRows(
			now, entity.InferenceIngestionProcessingJobStatusRunning, &eventID, &eventSequence, &now,
		))
	mock.ExpectRollback()

	repository := InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	result, err := repository.ApplyProcessingRunExecutionTransition(
		context.Background(),
		types.ApplyInferenceIngestionProcessingTransition{
			TenantID: "tenant-a", ProcessingRunID: "run-2", ExecutionID: "execution-1",
			CandidateID: "candidate-1", ModelName: "EchoModel",
			Status:  entity.InferenceIngestionProcessingJobStatusRunning,
			EventID: &eventID, EventSequence: &eventSequence,
		},
	)

	require.NoError(t, err)
	require.Equal(t, "replayed", result.Outcome)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyProcessingRunExecutionTransitionRollsBackExecutionWhenAggregateFails(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now().UTC()
	eventID := "event-running"
	eventSequence := int64(2)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WillReturnRows(processingRunStateRows(now, entity.InferenceIngestionProcessingRunPhaseQueued, 1, nil, nil))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WillReturnRows(processingExecutionStateRows(now, entity.InferenceIngestionProcessingJobStatusQueued, nil, nil, nil))
	mock.ExpectQuery("UPDATE ingestion_processing_jobs").
		WillReturnRows(processingExecutionStateRows(
			now, entity.InferenceIngestionProcessingJobStatusRunning, &eventID, &eventSequence, &now,
		))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WillReturnRows(processingExecutionStateRows(
			now, entity.InferenceIngestionProcessingJobStatusRunning, &eventID, &eventSequence, &now,
		))
	mock.ExpectQuery("UPDATE ingestion_processing_runs").WillReturnError(errors.New("aggregate failed"))
	mock.ExpectRollback()

	repository := InferenceProcessingRunRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	_, err = repository.ApplyProcessingRunExecutionTransition(
		context.Background(),
		types.ApplyInferenceIngestionProcessingTransition{
			TenantID: "tenant-a", ProcessingRunID: "run-2", ExecutionID: "execution-1",
			CandidateID: "candidate-1", ModelName: "EchoModel",
			Status:  entity.InferenceIngestionProcessingJobStatusRunning,
			EventID: &eventID, EventSequence: &eventSequence, StartedAt: &now,
		},
	)

	require.EqualError(t, err, apiError.DatabaseError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunLocksAllocatesAndCommits(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("tenant-a\x001.2.3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(run_number\\), 0\\) \\+ 1").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(sqlmock.NewRows([]string{"run_number"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").
		WithArgs("run-2", "tenant-a", "1.2.3", 2, entity.InferenceIngestionProcessingRunTriggerAuto, entity.InferenceIngestionProcessingRunPhaseQueued).
		WillReturnRows(processingRunRows(time.Now()))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	run, err := repository.CreateProcessingRun(context.Background(), types.CreateInferenceIngestionProcessingRun{
		ID: "run-2", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
	})

	require.NoError(t, err)
	require.Equal(t, 2, run.RunNumber)
	require.Equal(t, "tenant-a", run.TenantID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunMapsActiveRunConflict(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"run_number"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").
		WillReturnError(&pgconn.PgError{Code: "23505"})
	mock.ExpectRollback()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	_, err = repository.CreateProcessingRun(context.Background(), types.CreateInferenceIngestionProcessingRun{
		ID: "run-2", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunPlanCreatesRunAndExecutionsAtomically(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").
		WithArgs("tenant-a\x001.2.3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(emptyProcessingRunRows())
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(run_number\\), 0\\) \\+ 1").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(sqlmock.NewRows([]string{"run_number"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").
		WithArgs("run-2", "tenant-a", "1.2.3", 2, entity.InferenceIngestionProcessingRunTriggerAuto, entity.InferenceIngestionProcessingRunPhaseQueued).
		WillReturnRows(processingRunRows(now))
	mock.ExpectQuery("INSERT INTO ingestion_processing_jobs").
		WithArgs("execution-1", "run-2", "candidate-1", "tenant-a", "EchoModel", "v1", "US", entity.InferenceIngestionProcessingJobStatusPending).
		WillReturnRows(processingExecutionRows(now))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	modelVersion := "v1"
	modality := "US"
	result, err := repository.CreateProcessingRunPlan(context.Background(), types.CreateInferenceIngestionProcessingRunPlan{
		Run: types.CreateInferenceIngestionProcessingRun{
			ID: "run-2", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
		},
		Executions: []types.CreateInferenceIngestionProcessingExecution{{
			ID: "execution-1", CandidateID: "candidate-1", ModelName: "EchoModel",
			ModelVersion: &modelVersion, Modality: &modality,
		}},
	})

	require.NoError(t, err)
	require.True(t, result.Created)
	require.Equal(t, "run-2", result.Run.ID)
	require.Len(t, result.Executions, 1)
	require.Equal(t, "execution-1", result.Executions[0].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunPlanReusesActiveAutomaticPlan(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").
		WithArgs("tenant-a", "1.2.3").
		WillReturnRows(processingRunRows(now))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_jobs").
		WithArgs("run-2").
		WillReturnRows(processingExecutionRows(now))
	mock.ExpectCommit()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	result, err := repository.CreateProcessingRunPlan(context.Background(), types.CreateInferenceIngestionProcessingRunPlan{
		Run: types.CreateInferenceIngestionProcessingRun{
			ID: "unused", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
		},
	})

	require.NoError(t, err)
	require.False(t, result.Created)
	require.Equal(t, "run-2", result.Run.ID)
	require.Len(t, result.Executions, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProcessingRunPlanRejectsManualRunWhileActive(t *testing.T) {
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

func TestCreateProcessingRunPlanRollsBackWhenExecutionInsertFails(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	now := time.Now()

	mock.ExpectBegin()
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\* FROM ingestion_processing_runs").WillReturnRows(emptyProcessingRunRows())
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"run_number"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO ingestion_processing_runs").WillReturnRows(processingRunRows(now))
	mock.ExpectQuery("INSERT INTO ingestion_processing_jobs").WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	_, err = repository.CreateProcessingRunPlan(context.Background(), types.CreateInferenceIngestionProcessingRunPlan{
		Run: types.CreateInferenceIngestionProcessingRun{
			ID: "run-2", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued,
		},
		Executions: []types.CreateInferenceIngestionProcessingExecution{{
			ID: "execution-1", CandidateID: "candidate-1", ModelName: "EchoModel",
		}},
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSelectActiveProcessingRunIsTenantScoped(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "tenant_id = :tenant_id")
		require.Contains(t, query, "phase <> 'TERMINAL'")
		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		require.Equal(t, "1.2.3", arguments["study_instance_uid"])
		*target.(*entity.InferenceIngestionProcessingRun) = entity.InferenceIngestionProcessingRun{ID: "run-1"}
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	run, err := repository.SelectActiveProcessingRun(context.Background(), "tenant-a", "1.2.3")
	require.NoError(t, err)
	require.Equal(t, "run-1", run.ID)
}

func TestSelectProcessingRunByIDIsTenantScoped(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "tenant_id = :tenant_id")
		require.Contains(t, query, "id = :processing_run_id")
		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		require.Equal(t, "run-1", arguments["processing_run_id"])
		*target.(*entity.InferenceIngestionProcessingRun) = entity.InferenceIngestionProcessingRun{ID: "run-1", TenantID: "tenant-a"}
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	run, err := repository.SelectProcessingRun(context.Background(), "tenant-a", "run-1")
	require.NoError(t, err)
	require.Equal(t, "run-1", run.ID)
}

func TestListProcessingRunExecutionsIsTenantScoped(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.True(t, strings.Contains(query, "JOIN ingestion_processing_runs"))
		require.Contains(t, query, "runs.tenant_id = :tenant_id")
		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		require.Equal(t, "run-1", arguments["processing_run_id"])
		*target.(*[]entity.InferenceIngestionProcessingJob) = append(
			*target.(*[]entity.InferenceIngestionProcessingJob),
			entity.InferenceIngestionProcessingJob{ID: "execution-1"},
		)
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	executions, err := repository.ListProcessingRunExecutions(context.Background(), "tenant-a", "run-1")
	require.NoError(t, err)
	require.Len(t, executions, 1)
}

func TestSelectProcessingRunExecutionIsFullyScoped(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "runs.tenant_id = :tenant_id")
		require.Contains(t, query, "runs.id = :processing_run_id")
		require.Contains(t, query, "jobs.candidate_id = :candidate_id")
		require.Contains(t, query, "jobs.model_name = :model_name")
		arguments := model.(map[string]interface{})
		require.Equal(t, "tenant-a", arguments["tenant_id"])
		require.Equal(t, "run-1", arguments["processing_run_id"])
		require.Equal(t, "candidate-1", arguments["candidate_id"])
		require.Equal(t, "model-one", arguments["model_name"])
		*target.(*entity.InferenceIngestionProcessingJob) = entity.InferenceIngestionProcessingJob{ID: "execution-1"}
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	execution, err := repository.SelectProcessingRunExecution(
		context.Background(), "tenant-a", "run-1", "candidate-1", "model-one",
	)

	require.NoError(t, err)
	require.Equal(t, "execution-1", execution.ID)
}

func TestListProcessingRunHistoryUsesTenantStudyAndPagination(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "tenant_id = :tenant_id")
		require.Contains(t, query, "study_instance_uid = :study_instance_uid")
		require.Contains(t, query, "LIMIT :limit OFFSET :offset")
		arguments := model.(types.ListInferenceIngestionProcessingRuns)
		require.Equal(t, "tenant-a", arguments.TenantID)
		require.Equal(t, "1.2.3", arguments.StudyInstanceUID)
		require.Equal(t, 20, arguments.Limit)
		require.Equal(t, 40, arguments.Offset)
		*target.(*[]entity.InferenceIngestionProcessingRun) = append(
			*target.(*[]entity.InferenceIngestionProcessingRun),
			entity.InferenceIngestionProcessingRun{ID: "run-3", RunNumber: 3},
		)
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	runs, err := repository.ListProcessingRunHistory(context.Background(), types.ListInferenceIngestionProcessingRuns{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Limit: 20, Offset: 40,
	})
	require.NoError(t, err)
	require.Equal(t, "run-3", runs[0].ID)
}

func TestListProcessingRunsForReconciliationSelectsOnlyActiveStaleOrAttentionWork(t *testing.T) {
	staleBefore := time.Now().UTC().Add(-2 * time.Minute)
	handler := &processingRunTestHandler{}
	handler.query = func(query string, model interface{}, target interface{}) error {
		require.Contains(t, query, "WHERE runs.phase <> 'TERMINAL'")
		require.Contains(t, query, "runs.attention_required = TRUE")
		require.Contains(t, query, "jobs.status IN ('pending', 'queued', 'running')")
		require.Contains(t, query, "jobs.updated_at <= :active_stale_before")
		require.Contains(t, query, "ORDER BY runs.attention_required DESC, runs.updated_at ASC")
		require.Contains(t, query, "LIMIT :limit")

		arguments := model.(types.ListInferenceIngestionProcessingRunsForReconciliation)
		require.Equal(t, staleBefore, arguments.ActiveStaleBefore)
		require.Equal(t, 100, arguments.Limit)
		*target.(*[]entity.InferenceIngestionProcessingRun) = append(
			*target.(*[]entity.InferenceIngestionProcessingRun),
			entity.InferenceIngestionProcessingRun{
				ID: "run-attention", Phase: entity.InferenceIngestionProcessingRunPhaseQueued, AttentionRequired: true,
			},
			entity.InferenceIngestionProcessingRun{ID: "run-stale", Phase: entity.InferenceIngestionProcessingRunPhaseProcessing},
		)
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	runs, err := repository.ListProcessingRunsForReconciliation(
		context.Background(),
		types.ListInferenceIngestionProcessingRunsForReconciliation{
			ActiveStaleBefore: staleBefore,
			Limit:             100,
		},
	)

	require.NoError(t, err)
	require.Equal(t, []string{"run-attention", "run-stale"}, []string{runs[0].ID, runs[1].ID})
}

func TestUpdateProcessingRunAggregateMapsVersionConflict(t *testing.T) {
	queryCount := 0
	handler := &processingRunTestHandler{}
	handler.queryRow = func(query string, _ interface{}, _ interface{}) error {
		queryCount++
		if strings.HasPrefix(strings.TrimSpace(query), "UPDATE") {
			require.Contains(t, query, "tenant_id = :tenant_id")
			require.Contains(t, query, "version = :expected_version")
			return sql.ErrNoRows
		}
		return nil
	}

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	_, err := repository.UpdateProcessingRunAggregate(context.Background(), types.UpdateInferenceIngestionProcessingRunAggregate{
		ID: "run-1", TenantID: "tenant-a", ExpectedVersion: 1,
		Phase: entity.InferenceIngestionProcessingRunPhaseProcessing,
	})
	require.EqualError(t, err, apiError.DuplicateRecord)
	require.Equal(t, 2, queryCount)
}

func TestUpdateProcessingRunAggregateMapsMissingRecord(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(_ string, _ interface{}, _ interface{}) error { return sql.ErrNoRows }

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	_, err := repository.UpdateProcessingRunAggregate(context.Background(), types.UpdateInferenceIngestionProcessingRunAggregate{
		ID: "missing", TenantID: "tenant-a", ExpectedVersion: 1,
		Phase: entity.InferenceIngestionProcessingRunPhaseTerminal,
	})
	require.EqualError(t, err, apiError.MissingRecord)
}

func TestProcessingRunRepositoryMapsDatabaseFailure(t *testing.T) {
	handler := &processingRunTestHandler{}
	handler.queryRow = func(_ string, _ interface{}, _ interface{}) error { return errors.New("database unavailable") }

	repository := InferenceProcessingRunRepository{PostgresSQLDBHandlerInterface: handler}
	_, err := repository.SelectLatestProcessingRun(context.Background(), "tenant-a", "1.2.3")
	require.EqualError(t, err, apiError.DatabaseError)
}
