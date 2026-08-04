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

func processingRunRows(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "study_instance_uid", "run_number", "run_trigger", "phase",
		"outcome", "attention_required", "attention_reasons", "version", "started_at",
		"completed_at", "created_at", "updated_at",
	}).AddRow(
		"run-2", "tenant-a", "1.2.3", 2, "AUTO", "QUEUED",
		nil, false, []byte("[]"), int64(1), nil, nil, now, now,
	)
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
