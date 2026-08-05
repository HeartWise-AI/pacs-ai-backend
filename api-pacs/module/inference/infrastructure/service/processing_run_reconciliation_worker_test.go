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
	attempts           []repositoryTypes.RecordInferenceIngestionProcessingRunReconciliationAttempt
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

func (repository *reconciliationWorkerRunRepository) RecordProcessingRunReconciliationAttempt(
	_ context.Context,
	data repositoryTypes.RecordInferenceIngestionProcessingRunReconciliationAttempt,
) (entity.InferenceIngestionProcessingRun, error) {
	repository.attempts = append(repository.attempts, data)
	for index := range repository.runs {
		if repository.runs[index].ID != data.ID {
			continue
		}
		if data.Succeeded {
			repository.runs[index].ReconciliationFailureCount = 0
		} else {
			repository.runs[index].ReconciliationFailureCount++
		}
		repository.runs[index].LastReconciliationAt = &data.AttemptedAt
		return repository.runs[index], nil
	}
	return entity.InferenceIngestionProcessingRun{}, nil
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
	calls      []string
	jobIDCalls []string
	err        error
	jobIDErr   error
}

func (dispatcher *reconciliationWorkerDispatcher) GetJobByID(
	_ context.Context,
	tenantID, jobID string,
) (serviceTypes.StudyServiceJob, bool, error) {
	dispatcher.jobIDCalls = append(dispatcher.jobIDCalls, tenantID+":"+jobID)
	return serviceTypes.StudyServiceJob{}, false, dispatcher.jobIDErr
}

func (dispatcher *reconciliationWorkerDispatcher) GetJobsByCandidate(
	_ context.Context,
	tenantID, candidateID string,
) ([]serviceTypes.StudyServiceJob, error) {
	dispatcher.calls = append(dispatcher.calls, tenantID+":"+candidateID)
	return nil, dispatcher.err
}

func TestReconciliationWorkerQueriesOnlyPreciselyEligibleCandidates(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	studyServiceJobID := "python-job-missing"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{
			{
				ID: "run-stale", TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
				ReconciliationFailureCount: 2,
				AttentionRequired:          true,
				AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
					Code: entity.InferenceIngestionProcessingRunAttentionReconciliationFailed,
				}},
			},
			{ID: "run-fresh", TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseQueued},
		},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			"run-stale": {{
				ID: "execution-stale", CandidateID: "candidate-stale", Status: entity.InferenceIngestionProcessingJobStatusPending,
				StudyServiceJobID: &studyServiceJobID, UpdatedAt: now.Add(-3 * time.Minute),
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
	require.Equal(t, []string{"tenant-a:python-job-missing"}, dispatcher.jobIDCalls)
	require.Equal(t, []string{"tenant-a:candidate-stale"}, dispatcher.calls)
	require.Len(t, runRepository.attempts, 1)
	require.True(t, runRepository.attempts[0].Succeeded)
	require.Zero(t, runRepository.runs[0].ReconciliationFailureCount)
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

func TestReconciliationWorkerMarksThirdConsecutiveFailureForAttention(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationFailureThresholdEnv, "3")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: "run-stale", TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseProcessing,
			ReconciliationFailureCount: 2,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			"run-stale": {{
				ID: "execution-stale", CandidateID: "candidate-stale", Status: entity.InferenceIngestionProcessingJobStatusRunning,
				UpdatedAt: now.Add(-70 * time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		"candidate-stale": {ID: "candidate-stale", TenantID: "tenant-a"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{err: context.DeadlineExceeded}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Len(t, runRepository.attempts, 1)
	require.False(t, runRepository.attempts[0].Succeeded)
	require.Equal(t, 3, runRepository.runs[0].ReconciliationFailureCount)
	require.Len(t, runRepository.aggregateUpdates, 1)
	require.Contains(t, runRepository.aggregateUpdates[0].AttentionReasons,
		entity.InferenceIngestionProcessingRunAttentionReason{
			Code: entity.InferenceIngestionProcessingRunAttentionReconciliationFailed,
		},
	)
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

func TestValidateReconciledStudyServiceJobRejectsCorrelationMismatch(t *testing.T) {
	runID := "run-1"
	candidateID := "candidate-1"
	tenantID := "tenant-a"
	run := entity.InferenceIngestionProcessingRun{ID: runID, TenantID: tenantID}
	execution := entity.InferenceIngestionProcessingJob{CandidateID: candidateID, ModelName: "EchoPrime"}

	tests := []struct {
		name string
		job  serviceTypes.StudyServiceJob
	}{
		{name: "run", job: serviceTypes.StudyServiceJob{JobID: "job-1", ProcessingRunID: stringPointer("other-run"), CandidateID: &candidateID, TenantID: &tenantID, ModelName: "EchoPrime"}},
		{name: "candidate", job: serviceTypes.StudyServiceJob{JobID: "job-1", ProcessingRunID: &runID, CandidateID: stringPointer("other-candidate"), TenantID: &tenantID, ModelName: "EchoPrime"}},
		{name: "tenant", job: serviceTypes.StudyServiceJob{JobID: "job-1", ProcessingRunID: &runID, CandidateID: &candidateID, TenantID: stringPointer("other-tenant"), ModelName: "EchoPrime"}},
		{name: "model", job: serviceTypes.StudyServiceJob{JobID: "job-1", ProcessingRunID: &runID, CandidateID: &candidateID, TenantID: &tenantID, ModelName: "OtherModel"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateReconciledStudyServiceJob(run, execution, test.job))
		})
	}
}
