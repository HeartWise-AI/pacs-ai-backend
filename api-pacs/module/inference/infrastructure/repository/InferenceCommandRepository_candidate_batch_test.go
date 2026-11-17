package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/repository/types"
)

func manualCandidateBatch() []types.UpsertIngestionCandidate {
	return []types.UpsertIngestionCandidate{
		{ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: "job-a", StudyInstanceUID: "1.2.3"},
		{ID: "candidate-b", TenantID: "tenant-a", IngestionJobID: "job-b", StudyInstanceUID: "1.2.3"},
	}
}

func TestUpsertRetrievedIngestionCandidatesCommitsCompleteBatch(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer database.Close()

	mock.ExpectBegin()
	for range manualCandidateBatch() {
		mock.ExpectExec("INSERT INTO ingestion_candidates").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("UPDATE ingestion_candidates").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	repository := &InferenceCommandRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	require.NoError(t, repository.UpsertRetrievedIngestionCandidates(context.Background(), manualCandidateBatch()))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpsertRetrievedIngestionCandidatesRollsBackPartialBatch(t *testing.T) {
	database, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer database.Close()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO ingestion_candidates").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE ingestion_candidates").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO ingestion_candidates").WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	repository := &InferenceCommandRepository{PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")}}
	err = repository.UpsertRetrievedIngestionCandidates(context.Background(), manualCandidateBatch())
	require.EqualError(t, err, apiError.DatabaseError)
	require.NoError(t, mock.ExpectationsWereMet())
}
