package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

func TestInsertInferenceIngestionJobSerializesAndResolvesContainerRedirect(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	redirectRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"container_id", "model_version"}).
			AddRow("new-container", "2.0.0")
	}
	mock.ExpectQuery("SELECT").
		WithArgs("tenant-1", "old-container", "1.0.0").
		WillReturnRows(redirectRows())
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:new-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").
		WithArgs("tenant-1", "old-container", "1.0.0").
		WillReturnRows(redirectRows())
	arguments := make([]driver.Value, 16)
	for index := range arguments {
		arguments[index] = sqlmock.AnyArg()
	}
	arguments[3] = "new-container"
	arguments[6] = "2.0.0"
	mock.ExpectExec("INSERT INTO ingestion_jobs").
		WithArgs(arguments...).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	repository := InferenceCommandRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	err = repository.InsertInferenceIngestionJob(context.Background(), repositoryTypes.CreateInferenceIngestionJob{
		ID: "job-1", TenantID: "tenant-1", ContainerID: "old-container",
		DICOMModality: "XA", ModelID: "model-1", ModelName: "Model", ModelVersion: "1.0.0",
		Modalities: []string{"XA"}, ScheduleStartTimestamp: time.Now(), ScheduleEndTimestamp: time.Now(),
		Status: entity.InferenceIngestionJobStatusRunning,
	})
	if err != nil {
		t.Fatalf("insert failed: %v; expectations: %v", err, mock.ExpectationsWereMet())
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertInferenceIngestionJobHonorsCancelledContextDuringTransactionAcquisition(t *testing.T) {
	database, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := InferenceCommandRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = repository.InsertInferenceIngestionJob(ctx, repositoryTypes.CreateInferenceIngestionJob{
		ID: "job-1", TenantID: "tenant-1", ContainerID: "old-container",
		ModelID: "model-1", ModelName: "Model", ModelVersion: "1.0.0",
	})

	if err == nil || err.Error() != apiError.DatabaseError {
		t.Fatalf("cancelled ingestion transaction acquisition returned %v", err)
	}
}

func TestAcquireInferenceIngestionTargetHoldsSourceAndResolvedLocksUntilRelease(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	redirectRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"container_id", "model_version"}).
			AddRow("new-container", "2.0.0")
	}
	mock.ExpectQuery("SELECT").
		WithArgs("tenant-1", "old-container", "1.0.0").
		WillReturnRows(redirectRows())
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:new-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").
		WithArgs("tenant-1", "old-container", "1.0.0").
		WillReturnRows(redirectRows())
	mock.ExpectRollback()
	repository := InferenceCommandRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	containerID, version, release, err := repository.AcquireInferenceIngestionTarget(
		context.Background(), "tenant-1", "old-container", "1.0.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	if containerID != "new-container" || version != "2.0.0" {
		t.Fatalf("redirect was not resolved: container=%s version=%s", containerID, version)
	}
	release()
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireInferenceIngestionTargetHonorsCancelledContextDuringTransactionAcquisition(t *testing.T) {
	database, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := InferenceCommandRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err = repository.AcquireInferenceIngestionTarget(
		ctx, "tenant-1", "old-container", "1.0.0",
	)

	if err == nil || err.Error() != apiError.DatabaseError {
		t.Fatalf("cancelled transaction acquisition returned %v", err)
	}
}
