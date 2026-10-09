package repository

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

func TestRecoverableInferenceModelUpgradeStatesMatchStateMachine(t *testing.T) {
	states := []entity.InferenceModelUpgradeState{
		entity.InferenceModelUpgradeQueued,
		entity.InferenceModelUpgradePulling,
		entity.InferenceModelUpgradeValidating,
		entity.InferenceModelUpgradeDraining,
		entity.InferenceModelUpgradeActivating,
		entity.InferenceModelUpgradeVerifying,
		entity.InferenceModelUpgradeSucceeded,
		entity.InferenceModelUpgradeRollingBack,
		entity.InferenceModelUpgradeRolledBack,
		entity.InferenceModelUpgradeFailed,
		entity.InferenceModelUpgradeCancelled,
		entity.InferenceModelUpgradeDegraded,
	}
	recoverable := recoverableInferenceModelUpgradeStates()
	for _, state := range states {
		expected := !state.Terminal() || state == entity.InferenceModelUpgradeDegraded
		if slices.Contains(recoverable, string(state)) != expected {
			t.Fatalf("state %s recoverability does not match the state machine", state)
		}
	}
}

func TestInferenceRoutingLockTargetsLocksAliasesBeforePrimary(t *testing.T) {
	targets := inferenceRoutingLockTargets("old-container", []string{
		" stable-b ", "old-container", "stable-a", "stable-b", "",
	})
	expected := []string{"stable-a", "stable-b", "old-container"}
	if !reflect.DeepEqual(targets, expected) {
		t.Fatalf("routing locks are not deterministic alias-first order: %v", targets)
	}
}

func TestActivationCompensationRestoresDeploymentWithItsOutputModes(t *testing.T) {
	previous := entity.InferenceModelDeployment{ContainerID: "previous"}
	candidate := entity.InferenceModelDeployment{ContainerID: "candidate"}
	attempt := entity.InferenceModelUpgrade{
		Previous: previous, Candidate: &candidate,
		PreviousSupportedOutputModes:  []string{"JSON"},
		CandidateSupportedOutputModes: []string{"JSON", "HTML"},
	}

	deployment, modes := activationCompensation(attempt, candidate)
	if deployment.ContainerID != previous.ContainerID || !reflect.DeepEqual(modes, []string{"JSON"}) {
		t.Fatalf("forward compensation did not select previous compatibility: %+v %v", deployment, modes)
	}
	deployment, modes = activationCompensation(attempt, previous)
	if deployment.ContainerID != candidate.ContainerID || !reflect.DeepEqual(modes, []string{"JSON", "HTML"}) {
		t.Fatalf("rollback compensation did not select candidate compatibility: %+v %v", deployment, modes)
	}
}

func TestPermanentActivationCompensationErrorsStopRetrying(t *testing.T) {
	for _, message := range []string{
		apiError.InferenceUpgradeInvalid, apiError.DuplicateRecord, apiError.MissingRecord,
	} {
		if !permanentActivationCompensationError(errors.New(message)) {
			t.Fatalf("permanent compensation error was treated as retryable: %s", message)
		}
	}
	if permanentActivationCompensationError(errors.New(apiError.FirestoreError)) {
		t.Fatal("transient Firestore error was treated as permanent")
	}
}

func TestReconcileAmbiguousRoutingCommitReappliesIdempotentRouting(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM inference_model_container_redirects").
		WithArgs("tenant-1", "new-container", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE inference_model_container_redirects").
		WithArgs("new-container", "2.0.0", "tenant-1", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO inference_model_container_redirects").
		WithArgs("tenant-1", "old-container", "new-container", "2.0.0").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE ingestion_jobs").
		WithArgs("new-container", "2.0.0", "tenant-1", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	if err = repository.reconcileAmbiguousRoutingCommit(
		context.Background(),
		entity.InferenceModelUpgrade{TenantID: "tenant-1"},
		entity.InferenceModelDeployment{ContainerID: "new-container", ModelVersion: "2.0.0"},
		[]string{"old-container"},
	); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileAmbiguousRoutingCommitDoesNotCompensateAfterStatementFailure(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM inference_model_container_redirects").
		WithArgs("tenant-1", "new-container", "old-container").
		WillReturnError(errors.New("database temporarily unavailable"))
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM inference_model_container_redirects").
		WithArgs("tenant-1", "new-container", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE inference_model_container_redirects").
		WithArgs("new-container", "2.0.0", "tenant-1", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO inference_model_container_redirects").
		WithArgs("tenant-1", "old-container", "new-container", "2.0.0").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE ingestion_jobs").
		WithArgs("new-container", "2.0.0", "tenant-1", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}

	err = repository.reconcileAmbiguousRoutingCommit(
		context.Background(),
		entity.InferenceModelUpgrade{TenantID: "tenant-1"},
		entity.InferenceModelDeployment{ContainerID: "new-container", ModelVersion: "2.0.0"},
		[]string{"old-container"},
	)

	if err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileAmbiguousRoutingCommitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{},
	}
	err := repository.reconcileAmbiguousRoutingCommit(
		ctx,
		entity.InferenceModelUpgrade{TenantID: "tenant-1"},
		entity.InferenceModelDeployment{ContainerID: "new-container", ModelVersion: "2.0.0"},
		[]string{"old-container"},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("routing reconciliation ignored cancellation: %v", err)
	}
}

func TestLegacyDeploymentBackfillsExistingRegistrationIdentity(t *testing.T) {
	model := entity.InferenceModel{
		ContainerID: "legacy-container",
		DockerImage: "heartwisehub/legacy-model:1.0.0",
	}
	deployment := legacyDeployment(model)
	if deployment.ContainerID != model.ContainerID || deployment.ImageReference != model.DockerImage {
		t.Fatalf("legacy identity was not retained: %+v", deployment)
	}
}

func TestLegacyDeploymentPrefersPersistedImmutableSnapshot(t *testing.T) {
	persisted := &entity.InferenceModelDeployment{
		ContainerID: "immutable-container", ImageReference: "heartwisehub/model:2.0.0",
		ImageDigest: "sha256:digest", LocalImageID: "sha256:local", ModelVersion: "2.0.0",
	}
	model := entity.InferenceModel{
		ContainerID: persisted.ContainerID, DockerImage: persisted.ImageReference,
		Deployment: persisted,
	}
	deployment := legacyDeployment(model)
	if deployment != *persisted {
		t.Fatalf("persisted deployment snapshot was not preserved: %+v", deployment)
	}
}

func TestLegacyDeploymentRejectsSnapshotThatDisagreesWithLiveRegistration(t *testing.T) {
	model := entity.InferenceModel{
		ContainerID: "manually-updated-container", DockerImage: "heartwisehub/model:3.0.0",
		Deployment: &entity.InferenceModelDeployment{
			ContainerID: "stale-container", ImageReference: "heartwisehub/model:2.0.0",
			ImageDigest: "sha256:stale",
		},
	}
	deployment := legacyDeployment(model)
	if deployment.ContainerID != model.ContainerID || deployment.ImageReference != model.DockerImage || deployment.ImageDigest != "" {
		t.Fatalf("stale deployment snapshot overrode the live registration: %+v", deployment)
	}
}

func TestRetargetInferenceIngestionJobsUpdatesAllMatchingTargetsTransactionally(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("tenant-1:old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM inference_model_container_redirects").
		WithArgs("tenant-1", "new-container", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE inference_model_container_redirects").
		WithArgs("new-container", "2.0.0", "tenant-1", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO inference_model_container_redirects").
		WithArgs("tenant-1", "old-container", "new-container", "2.0.0").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE ingestion_jobs").
		WithArgs("new-container", "2.0.0", "tenant-1", "old-container").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	if err = repository.RetargetInferenceIngestionJobs(
		context.Background(), "tenant-1", "old-container", "new-container", "2.0.0",
	); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetargetInferenceIngestionJobsHonorsCancelledContextDuringTransactionAcquisition(t *testing.T) {
	database, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{db: sqlx.NewDb(database, "sqlmock")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = repository.RetargetInferenceIngestionJobs(
		ctx, "tenant-1", "old-container", "new-container", "2.0.0",
	)

	if err == nil || err.Error() != apiError.DatabaseError {
		t.Fatalf("cancelled rollback transaction acquisition returned %v", err)
	}
}

func TestHasActiveInferenceProcessingJobsScopesNonTerminalWorkToDeployment(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "cleanup")
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{queryRowContext: func(queryCtx context.Context, query string, model interface{}, target interface{}) error {
			if queryCtx != ctx {
				t.Fatal("cleanup context was not propagated to the active-job query")
			}
			if !strings.Contains(query, "status IN ('pending', 'queued', 'running')") {
				t.Fatalf("active status filter is missing: %s", query)
			}
			parameters := model.(map[string]interface{})
			if parameters["tenant_id"] != "tenant-1" || parameters["model_name"] != "Model" || parameters["model_version"] != "1.0.0" {
				t.Fatalf("unexpected processing-job scope: %+v", parameters)
			}
			*(target.(*bool)) = true
			return nil
		}},
	}
	active, err := repository.HasActiveInferenceProcessingJobs(
		ctx, "tenant-1", "Model", "1.0.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("active processing work was not detected")
	}
}

func TestHasActiveInferenceProcessingJobsPropagatesCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queryCalled := false
	repository := InferenceModelUpgradeRepository{
		PostgresSQLDBHandlerInterface: &processingRunTestHandler{queryRowContext: func(queryCtx context.Context, _ string, _ interface{}, _ interface{}) error {
			queryCalled = true
			return queryCtx.Err()
		}},
	}

	active, err := repository.HasActiveInferenceProcessingJobs(ctx, "tenant-1", "Model", "1.0.0")

	if err == nil || err.Error() != apiError.DatabaseError {
		t.Fatalf("expected database error for cancelled query, got %v", err)
	}
	if active {
		t.Fatal("cancelled query unexpectedly reported active work")
	}
	if !queryCalled {
		t.Fatal("context-aware query was not called")
	}
}
