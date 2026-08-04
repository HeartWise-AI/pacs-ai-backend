package repository

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/jackc/pgx/v5/pgconn"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceProcessingRunRepository persists processing runs and their executions.
type InferenceProcessingRunRepository struct {
	postgresqlTypes.PostgresSQLDBHandlerInterface
}

func processingRunError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New(apiError.MissingRecord)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return errors.New(apiError.DuplicateRecord)
	}
	log.Println(err)
	return errors.New(apiError.DatabaseError)
}

// CreateProcessingRun atomically allocates the next study-local run number and inserts the run.
func (repository *InferenceProcessingRunRepository) CreateProcessingRun(ctx context.Context, data types.CreateInferenceIngestionProcessingRun) (entity.InferenceIngestionProcessingRun, error) {
	tx, err := repository.PostgresSQLDBHandlerInterface.Begin()
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}
	defer tx.Rollback()

	lockKey := data.TenantID + "\x00" + data.StudyInstanceUID
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}

	var runNumber int
	err = tx.GetContext(ctx, &runNumber, `
		SELECT COALESCE(MAX(run_number), 0) + 1
		FROM ingestion_processing_runs
		WHERE tenant_id = $1 AND study_instance_uid = $2
	`, data.TenantID, data.StudyInstanceUID)
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}

	var run entity.InferenceIngestionProcessingRun
	err = tx.GetContext(ctx, &run, `
		INSERT INTO ingestion_processing_runs (
			id, tenant_id, study_instance_uid, run_number, run_trigger, phase
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING *
	`, data.ID, data.TenantID, data.StudyInstanceUID, runNumber, data.RunTrigger, data.Phase)
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}

	if err = tx.Commit(); err != nil {
		return entity.InferenceIngestionProcessingRun{}, processingRunError(err)
	}
	return run, nil
}

// CreateProcessingRunPlan atomically freezes a run and its expected executions.
// An automatic request reuses the active plan. A manual request conflicts until the active run terminates.
func (repository *InferenceProcessingRunRepository) CreateProcessingRunPlan(ctx context.Context, data types.CreateInferenceIngestionProcessingRunPlan) (types.CreateInferenceIngestionProcessingRunPlanResult, error) {
	tx, err := repository.PostgresSQLDBHandlerInterface.Begin()
	if err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}
	defer tx.Rollback()

	lockKey := data.Run.TenantID + "\x00" + data.Run.StudyInstanceUID
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	var activeRun entity.InferenceIngestionProcessingRun
	err = tx.GetContext(ctx, &activeRun, `
		SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = $1 AND study_instance_uid = $2 AND phase <> 'TERMINAL'
		ORDER BY run_number DESC LIMIT 1
	`, data.Run.TenantID, data.Run.StudyInstanceUID)
	if err == nil {
		if data.Run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerAuto {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, errors.New(apiError.DuplicateRecord)
		}

		executions := make([]entity.InferenceIngestionProcessingJob, 0)
		if err = tx.SelectContext(ctx, &executions, `
			SELECT * FROM ingestion_processing_jobs
			WHERE processing_run_id = $1
			ORDER BY created_at ASC, id ASC
		`, activeRun.ID); err != nil {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
		}

		if err = tx.Commit(); err != nil {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
		}
		return types.CreateInferenceIngestionProcessingRunPlanResult{
			Run: activeRun, Executions: executions, Created: false,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	var runNumber int
	if err = tx.GetContext(ctx, &runNumber, `
		SELECT COALESCE(MAX(run_number), 0) + 1
		FROM ingestion_processing_runs
		WHERE tenant_id = $1 AND study_instance_uid = $2
	`, data.Run.TenantID, data.Run.StudyInstanceUID); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	var run entity.InferenceIngestionProcessingRun
	if err = tx.GetContext(ctx, &run, `
		INSERT INTO ingestion_processing_runs (
			id, tenant_id, study_instance_uid, run_number, run_trigger, phase
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING *
	`, data.Run.ID, data.Run.TenantID, data.Run.StudyInstanceUID, runNumber, data.Run.RunTrigger, data.Run.Phase); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}

	executions := make([]entity.InferenceIngestionProcessingJob, 0, len(data.Executions))
	for _, expected := range data.Executions {
		var execution entity.InferenceIngestionProcessingJob
		if err = tx.GetContext(ctx, &execution, `
			INSERT INTO ingestion_processing_jobs (
				id, processing_run_id, candidate_id, tenant_id, model_name,
				model_version, modality, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING *
		`, expected.ID, run.ID, expected.CandidateID, run.TenantID, expected.ModelName,
			expected.ModelVersion, expected.Modality, entity.InferenceIngestionProcessingJobStatusPending); err != nil {
			return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
		}
		executions = append(executions, execution)
	}

	if err = tx.Commit(); err != nil {
		return types.CreateInferenceIngestionProcessingRunPlanResult{}, processingRunError(err)
	}
	return types.CreateInferenceIngestionProcessingRunPlanResult{
		Run: run, Executions: executions, Created: true,
	}, nil
}

func (repository *InferenceProcessingRunRepository) selectStudyRun(ctx context.Context, tenantID, studyInstanceUID, suffix string) (entity.InferenceIngestionProcessingRun, error) {
	var run entity.InferenceIngestionProcessingRun
	query := `SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = :tenant_id AND study_instance_uid = :study_instance_uid ` + suffix
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(query, map[string]interface{}{
		"tenant_id": tenantID, "study_instance_uid": studyInstanceUID,
	}, &run)
	return run, processingRunError(err)
}

// SelectActiveProcessingRun returns the tenant-scoped non-terminal run for a study.
func (repository *InferenceProcessingRunRepository) SelectActiveProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	return repository.selectStudyRun(ctx, tenantID, studyInstanceUID, "AND phase <> 'TERMINAL' ORDER BY run_number DESC LIMIT 1")
}

// SelectLatestProcessingRun returns the newest tenant-scoped run for a study.
func (repository *InferenceProcessingRunRepository) SelectLatestProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	return repository.selectStudyRun(ctx, tenantID, studyInstanceUID, "ORDER BY run_number DESC LIMIT 1")
}

// ListProcessingRunHistory returns tenant-scoped runs ordered newest first.
func (repository *InferenceProcessingRunRepository) ListProcessingRunHistory(ctx context.Context, data types.ListInferenceIngestionProcessingRuns) ([]entity.InferenceIngestionProcessingRun, error) {
	runs := make([]entity.InferenceIngestionProcessingRun, 0)
	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT * FROM ingestion_processing_runs
		WHERE tenant_id = :tenant_id AND study_instance_uid = :study_instance_uid
		ORDER BY run_number DESC LIMIT :limit OFFSET :offset
	`, data, &runs)
	return runs, processingRunError(err)
}

// ListProcessingRunExecutions returns the expected model executions for a tenant-scoped run.
func (repository *InferenceProcessingRunRepository) ListProcessingRunExecutions(ctx context.Context, tenantID, processingRunID string) ([]entity.InferenceIngestionProcessingJob, error) {
	executions := make([]entity.InferenceIngestionProcessingJob, 0)
	err := repository.PostgresSQLDBHandlerInterface.Query(`
		SELECT jobs.* FROM ingestion_processing_jobs jobs
		JOIN ingestion_processing_runs runs ON runs.id = jobs.processing_run_id
		WHERE runs.tenant_id = :tenant_id AND runs.id = :processing_run_id
		ORDER BY jobs.created_at ASC, jobs.id ASC
	`, map[string]interface{}{"tenant_id": tenantID, "processing_run_id": processingRunID}, &executions)
	return executions, processingRunError(err)
}

// UpdateProcessingRunAggregate applies an optimistic versioned aggregate update.
func (repository *InferenceProcessingRunRepository) UpdateProcessingRunAggregate(ctx context.Context, data types.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error) {
	var run entity.InferenceIngestionProcessingRun
	err := repository.PostgresSQLDBHandlerInterface.QueryRow(`
		UPDATE ingestion_processing_runs SET
			phase = :phase, outcome = :outcome,
			attention_required = :attention_required, attention_reasons = :attention_reasons,
			started_at = :started_at, completed_at = :completed_at, version = version + 1
		WHERE id = :id AND tenant_id = :tenant_id AND version = :expected_version
		RETURNING *
	`, data, &run)
	if !errors.Is(err, sql.ErrNoRows) {
		return run, processingRunError(err)
	}

	var existing entity.InferenceIngestionProcessingRun
	lookupErr := repository.PostgresSQLDBHandlerInterface.QueryRow(
		"SELECT * FROM ingestion_processing_runs WHERE id = :id AND tenant_id = :tenant_id",
		map[string]interface{}{"id": data.ID, "tenant_id": data.TenantID}, &existing,
	)
	if lookupErr == nil {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.DuplicateRecord)
	}
	return entity.InferenceIngestionProcessingRun{}, processingRunError(lookupErr)
}
