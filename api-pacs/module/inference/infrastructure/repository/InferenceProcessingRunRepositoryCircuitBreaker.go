package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceProcessingRunRepositoryCircuitBreaker decorates processing-run persistence.
type InferenceProcessingRunRepositoryCircuitBreaker struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
}

var _ domainRepository.InferenceProcessingRunRepositoryInterface = (*InferenceProcessingRunRepository)(nil)
var _ domainRepository.InferenceProcessingRunRepositoryInterface = (*InferenceProcessingRunRepositoryCircuitBreaker)(nil)

func withProcessingRunCircuit[T any](name string, operation func() (T, error)) (T, error) {
	output := make(chan T, 1)
	errChan := make(chan error, 1)
	hystrix.ConfigureCommand(name, config.Settings())
	errorsChannel := hystrix.Go(name, func() error {
		value, err := operation()
		if err != nil {
			errChan <- err
			return nil
		}
		output <- value
		return nil
	}, nil)

	var zero T
	select {
	case value := <-output:
		return value, nil
	case err := <-errChan:
		return zero, err
	case err := <-errorsChannel:
		return zero, err
	}
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ApplyProcessingRunExecutionTransition(
	ctx context.Context,
	data types.ApplyInferenceIngestionProcessingTransition,
) (types.ApplyInferenceIngestionProcessingTransitionResult, error) {
	return withProcessingRunCircuit(
		"apply_processing_run_execution_transition",
		func() (types.ApplyInferenceIngestionProcessingTransitionResult, error) {
			return repository.InferenceProcessingRunRepositoryInterface.ApplyProcessingRunExecutionTransition(ctx, data)
		},
	)
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) CreateProcessingRun(ctx context.Context, data types.CreateInferenceIngestionProcessingRun) (entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("create_processing_run", func() (entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.CreateProcessingRun(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) CreateProcessingRunPlan(ctx context.Context, data types.CreateInferenceIngestionProcessingRunPlan) (types.CreateInferenceIngestionProcessingRunPlanResult, error) {
	return withProcessingRunCircuit("create_processing_run_plan", func() (types.CreateInferenceIngestionProcessingRunPlanResult, error) {
		return repository.InferenceProcessingRunRepositoryInterface.CreateProcessingRunPlan(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) SelectActiveProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("select_active_processing_run", func() (entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.SelectActiveProcessingRun(ctx, tenantID, studyInstanceUID)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) SelectProcessingRun(ctx context.Context, tenantID, processingRunID string) (entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("select_processing_run", func() (entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, processingRunID)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) SelectLatestProcessingRun(ctx context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("select_latest_processing_run", func() (entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.SelectLatestProcessingRun(ctx, tenantID, studyInstanceUID)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ListProcessingRunHistory(ctx context.Context, data types.ListInferenceIngestionProcessingRuns) ([]entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("list_processing_run_history", func() ([]entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ListProcessingRunHistory(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ListProcessingRunHistoryPage(ctx context.Context, data types.ListInferenceIngestionProcessingRuns) (types.InferenceIngestionProcessingRunHistoryPage, error) {
	return withProcessingRunCircuit("list_processing_run_history_page", func() (types.InferenceIngestionProcessingRunHistoryPage, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ListProcessingRunHistoryPage(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ListProcessingRunsForReconciliation(
	ctx context.Context,
	data types.ListInferenceIngestionProcessingRunsForReconciliation,
) ([]entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("list_processing_runs_for_reconciliation", func() ([]entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ListProcessingRunsForReconciliation(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ListLegacyProcessingRunBackfillRows(ctx context.Context) ([]types.LegacyProcessingRunBackfillRow, error) {
	return withProcessingRunCircuit("list_legacy_processing_run_backfill_rows", func() ([]types.LegacyProcessingRunBackfillRow, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ListLegacyProcessingRunBackfillRows(ctx)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) LoadLegacyProcessingRunVerificationSnapshot(ctx context.Context) (types.LegacyProcessingRunVerificationSnapshot, error) {
	return withProcessingRunCircuit("load_legacy_processing_run_verification_snapshot", func() (types.LegacyProcessingRunVerificationSnapshot, error) {
		return repository.InferenceProcessingRunRepositoryInterface.LoadLegacyProcessingRunVerificationSnapshot(ctx)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ImportLegacyProcessingRun(ctx context.Context, data types.ImportLegacyProcessingRun) (types.ImportLegacyProcessingRunResult, error) {
	return withProcessingRunCircuit("import_legacy_processing_run", func() (types.ImportLegacyProcessingRunResult, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ImportLegacyProcessingRun(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) RollbackLegacyProcessingRun(ctx context.Context, data types.RollbackLegacyProcessingRun) (types.RollbackLegacyProcessingRunResult, error) {
	return withProcessingRunCircuit("rollback_legacy_processing_run", func() (types.RollbackLegacyProcessingRunResult, error) {
		return repository.InferenceProcessingRunRepositoryInterface.RollbackLegacyProcessingRun(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) RecordProcessingRunReconciliationAttempt(
	ctx context.Context,
	data types.RecordInferenceIngestionProcessingRunReconciliationAttempt,
) (entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("record_processing_run_reconciliation_attempt", func() (entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.RecordProcessingRunReconciliationAttempt(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ListProcessingRunExecutions(ctx context.Context, tenantID, processingRunID string) ([]entity.InferenceIngestionProcessingJob, error) {
	return withProcessingRunCircuit("list_processing_run_executions", func() ([]entity.InferenceIngestionProcessingJob, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutions(ctx, tenantID, processingRunID)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) ListProcessingRunExecutionsByRunIDs(ctx context.Context, data types.ListInferenceIngestionProcessingRunExecutions) ([]entity.InferenceIngestionProcessingJob, error) {
	return withProcessingRunCircuit("list_processing_run_executions_by_run_ids", func() ([]entity.InferenceIngestionProcessingJob, error) {
		return repository.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutionsByRunIDs(ctx, data)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) SelectProcessingRunExecution(ctx context.Context, tenantID, processingRunID, candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error) {
	return withProcessingRunCircuit("select_processing_run_execution", func() (entity.InferenceIngestionProcessingJob, error) {
		return repository.InferenceProcessingRunRepositoryInterface.SelectProcessingRunExecution(ctx, tenantID, processingRunID, candidateID, modelName)
	})
}

func (repository *InferenceProcessingRunRepositoryCircuitBreaker) UpdateProcessingRunAggregate(ctx context.Context, data types.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error) {
	return withProcessingRunCircuit("update_processing_run_aggregate", func() (entity.InferenceIngestionProcessingRun, error) {
		return repository.InferenceProcessingRunRepositoryInterface.UpdateProcessingRunAggregate(ctx, data)
	})
}
