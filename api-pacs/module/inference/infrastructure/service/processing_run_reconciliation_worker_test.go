package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	inferenceApplication "api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type reconciliationWorkerRunRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	runs               []entity.InferenceIngestionProcessingRun
	executions         map[string][]entity.InferenceIngestionProcessingJob
	listInput          repositoryTypes.ListInferenceIngestionProcessingRunsForReconciliation
	executionListCalls int
	aggregateUpdates   []repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate
}

func (repository *reconciliationWorkerRunRepository) SelectProcessingRun(
	_ context.Context,
	_, processingRunID string,
) (entity.InferenceIngestionProcessingRun, error) {
	for _, run := range repository.runs {
		if run.ID == processingRunID {
			return run, nil
		}
	}
	return entity.InferenceIngestionProcessingRun{}, nil
}

func (repository *reconciliationWorkerRunRepository) UpdateProcessingRunAggregate(
	_ context.Context,
	data repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate,
) (entity.InferenceIngestionProcessingRun, error) {
	repository.aggregateUpdates = append(repository.aggregateUpdates, data)
	return entity.InferenceIngestionProcessingRun{
		ID: data.ID, TenantID: data.TenantID, Phase: data.Phase,
		AttentionRequired: data.AttentionRequired, AttentionReasons: data.AttentionReasons,
	}, nil
}

func (repository *reconciliationWorkerRunRepository) ListProcessingRunsForReconciliation(
	_ context.Context,
	data repositoryTypes.ListInferenceIngestionProcessingRunsForReconciliation,
) ([]entity.InferenceIngestionProcessingRun, error) {
	repository.listInput = data
	return repository.runs, nil
}

func (repository *reconciliationWorkerRunRepository) ListProcessingRunExecutions(
	_ context.Context,
	_, processingRunID string,
) ([]entity.InferenceIngestionProcessingJob, error) {
	repository.executionListCalls++
	return repository.executions[processingRunID], nil
}

type reconciliationWorkerQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	candidates map[string]entity.InferenceIngestionCandidate
	calls      []string
}

func (repository *reconciliationWorkerQueryRepository) SelectInferenceIngestionCandidateByID(id string) (entity.InferenceIngestionCandidate, error) {
	repository.calls = append(repository.calls, id)
	return repository.candidates[id], nil
}

type reconciliationWorkerDispatcher struct {
	inferenceApplication.ProcessingDispatcherInterface
	calls []string
}

func (dispatcher *reconciliationWorkerDispatcher) GetJobsByCandidate(
	_ context.Context,
	tenantID, candidateID string,
) ([]serviceTypes.StudyServiceJob, error) {
	dispatcher.calls = append(dispatcher.calls, tenantID+":"+candidateID)
	return nil, nil
}

func TestReconciliationWorkerQueriesOnlyPreciselyEligibleCandidates(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{
			{ID: "run-stale", TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseQueued},
			{ID: "run-fresh", TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseQueued},
		},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			"run-stale": {{
				ID: "execution-stale", CandidateID: "candidate-stale", Status: entity.InferenceIngestionProcessingJobStatusPending,
				UpdatedAt: now.Add(-3 * time.Minute),
			}},
			"run-fresh": {{
				ID: "execution-fresh", CandidateID: "candidate-fresh", Status: entity.InferenceIngestionProcessingJobStatusPending,
				UpdatedAt: now.Add(-time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		"candidate-stale": {ID: "candidate-stale", TenantID: "tenant-a"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Equal(t, processingReconciliationBatchLimit, runRepository.listInput.Limit)
	require.Equal(t, 4, runRepository.executionListCalls)
	require.Equal(t, []string{"candidate-stale"}, queryRepository.calls)
	require.Equal(t, []string{"tenant-a:candidate-stale"}, dispatcher.calls)
	require.Len(t, runRepository.aggregateUpdates, 1)
	require.True(t, runRepository.aggregateUpdates[0].AttentionRequired)
	require.Equal(t, []string{
		entity.InferenceIngestionProcessingRunAttentionPendingStale,
		entity.InferenceIngestionProcessingRunAttentionStudyServiceJobMissing,
	}, []string{
		runRepository.aggregateUpdates[0].AttentionReasons[0].Code,
		runRepository.aggregateUpdates[0].AttentionReasons[1].Code,
	})
}

func TestReconciliationCandidateIDsDeduplicatesCandidates(t *testing.T) {
	evaluation := processingRunReconciliationEvaluation{
		Eligible: true,
		StaleExecutions: []processingExecutionReconciliationTarget{
			{Execution: entity.InferenceIngestionProcessingJob{CandidateID: "candidate-1"}},
			{Execution: entity.InferenceIngestionProcessingJob{CandidateID: "candidate-1"}},
			{Execution: entity.InferenceIngestionProcessingJob{CandidateID: "candidate-2"}},
		},
	}

	require.Equal(t, []string{"candidate-1", "candidate-2"}, reconciliationCandidateIDs(evaluation, nil))
}

func TestRemoveProcessingRunAttentionReasonsClearsOnlyManagedCodes(t *testing.T) {
	reasons := entity.InferenceIngestionProcessingRunAttentionReasons{
		{Code: entity.InferenceIngestionProcessingRunAttentionPendingStale},
		{Code: entity.InferenceIngestionProcessingRunAttentionStudyServiceJobMissing},
		{Code: "MANUAL_REVIEW_REQUIRED"},
	}

	filtered := removeProcessingRunAttentionReasons(reasons, reconciliationManagedAttentionReasonCodes)

	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionReasons{
		{Code: "MANUAL_REVIEW_REQUIRED"},
	}, filtered)
}
