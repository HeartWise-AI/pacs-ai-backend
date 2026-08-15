package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
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

func (repository *reconciliationWorkerRunRepository) SelectProcessingRunExecution(
	_ context.Context,
	_, processingRunID, candidateID, modelName string,
) (entity.InferenceIngestionProcessingJob, error) {
	for _, execution := range repository.executions[processingRunID] {
		if execution.CandidateID == candidateID && execution.ModelName == modelName {
			return execution, nil
		}
	}
	return entity.InferenceIngestionProcessingJob{}, errors.New(apiError.MissingRecord)
}

func (repository *reconciliationWorkerRunRepository) ApplyProcessingRunExecutionTransition(
	_ context.Context,
	data repositoryTypes.ApplyInferenceIngestionProcessingTransition,
) (repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult, error) {
	executions := repository.executions[data.ProcessingRunID]
	for index := range executions {
		if executions[index].ID != data.ExecutionID {
			continue
		}
		executions[index].Status = data.Status
		executions[index].StudyServiceJobID = data.StudyServiceJobID
		executions[index].StartedAt = data.StartedAt
		executions[index].CompletedAt = data.CompletedAt
		executions[index].UpdatedAt = time.Now()
	}
	repository.executions[data.ProcessingRunID] = executions

	for index := range repository.runs {
		if repository.runs[index].ID != data.ProcessingRunID {
			continue
		}
		aggregate := entity.AggregateInferenceIngestionProcessingRun(
			entity.InferenceIngestionProcessingRunAggregationInput{
				Run: repository.runs[index], Executions: executions,
			},
		)
		repository.runs[index].Phase = aggregate.Phase
		repository.runs[index].Outcome = aggregate.Outcome
		repository.runs[index].AttentionRequired = aggregate.AttentionRequired
		repository.runs[index].AttentionReasons = aggregate.AttentionReasons
		repository.runs[index].Version = aggregate.NextVersion
		return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{
			Outcome: "applied", Changed: true, Execution: executions[0], Run: repository.runs[index], Counts: aggregate.Counts,
		}, nil
	}
	return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{}, errors.New(apiError.MissingRecord)
}

func (repository *reconciliationWorkerRunRepository) UpdateProcessingRunAggregate(
	_ context.Context,
	data repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate,
) (entity.InferenceIngestionProcessingRun, error) {
	repository.aggregateUpdates = append(repository.aggregateUpdates, data)
	updated := entity.InferenceIngestionProcessingRun{ID: data.ID, TenantID: data.TenantID}
	for index := range repository.runs {
		if repository.runs[index].ID == data.ID {
			updated = repository.runs[index]
			updated.Phase = data.Phase
			updated.Outcome = data.Outcome
			updated.AttentionRequired = data.AttentionRequired
			updated.AttentionReasons = data.AttentionReasons
			updated.Version++
			repository.runs[index] = updated
			break
		}
	}
	return updated, nil
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
	calls         []string
	jobIDCalls    []string
	runCalls      []string
	err           error
	jobIDErr      error
	runErr        error
	deadLetterErr error
	jobsByID      map[string]serviceTypes.StudyServiceJob
	jobsByRunID   map[string][]serviceTypes.StudyServiceJob
	deadLetters   []serviceTypes.StudyServiceCallbackDeadLetter
}

type reconciliationMetricsRecorderTestDouble struct {
	cycles []ProcessingReconciliationCycleMetrics
}

func (recorder *reconciliationMetricsRecorderTestDouble) RecordProcessingReconciliationCycle(
	metrics ProcessingReconciliationCycleMetrics,
) {
	recorder.cycles = append(recorder.cycles, metrics)
}

func (dispatcher *reconciliationWorkerDispatcher) GetJobByID(
	_ context.Context,
	tenantID, jobID string,
) (serviceTypes.StudyServiceJob, bool, error) {
	dispatcher.jobIDCalls = append(dispatcher.jobIDCalls, tenantID+":"+jobID)
	if job, found := dispatcher.jobsByID[jobID]; found {
		return job, true, nil
	}
	return serviceTypes.StudyServiceJob{}, false, dispatcher.jobIDErr
}

func (dispatcher *reconciliationWorkerDispatcher) GetJobsByProcessingRun(
	_ context.Context,
	tenantID, processingRunID string,
) ([]serviceTypes.StudyServiceJob, error) {
	dispatcher.runCalls = append(dispatcher.runCalls, tenantID+":"+processingRunID)
	return dispatcher.jobsByRunID[processingRunID], dispatcher.runErr
}

func (dispatcher *reconciliationWorkerDispatcher) GetJobsByCandidate(
	_ context.Context,
	tenantID, candidateID string,
) ([]serviceTypes.StudyServiceJob, error) {
	dispatcher.calls = append(dispatcher.calls, tenantID+":"+candidateID)
	return nil, dispatcher.err
}

func (dispatcher *reconciliationWorkerDispatcher) GetCallbackDeadLetters(
	_ context.Context,
	_ string,
) ([]serviceTypes.StudyServiceCallbackDeadLetter, error) {
	return dispatcher.deadLetters, dispatcher.deadLetterErr
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
	metricsRecorder := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
		ProcessingReconciliationMetricsRecorder:   metricsRecorder,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Equal(t, processingReconciliationBatchLimit, runRepository.listInput.Limit)
	require.Equal(t, 4, runRepository.executionListCalls)
	require.Equal(t, []string{"candidate-stale"}, queryRepository.calls)
	require.Equal(t, []string{"tenant-a:python-job-missing"}, dispatcher.jobIDCalls)
	require.Equal(t, []string{"tenant-a:run-stale"}, dispatcher.runCalls)
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
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Checked: 2, Unresolved: 1}}, metricsRecorder.cycles)
}

func TestReconciliationWorkerRepairsMissedTerminalCallbackByExactJobID(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	completedAt := now.Add(-time.Minute)
	runID := "run-1"
	candidateID := "candidate-1"
	tenantID := "tenant-a"
	jobID := "python-job-1"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: tenantID, StudyInstanceUID: "study-1",
			Phase: entity.InferenceIngestionProcessingRunPhaseProcessing, Version: 3,
			AttentionRequired: true,
			AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
				Code: entity.InferenceIngestionProcessingRunAttentionProcessingStale,
			}},
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-1", ProcessingRunID: &runID, CandidateID: candidateID, TenantID: tenantID,
				ModelName: "EchoPrime", Status: entity.InferenceIngestionProcessingJobStatusRunning,
				StudyServiceJobID: &jobID, UpdatedAt: now.Add(-70 * time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		candidateID: {ID: candidateID, TenantID: tenantID, StudyInstanceUID: "study-1"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{jobsByID: map[string]serviceTypes.StudyServiceJob{
		jobID: {
			JobID: jobID, StudyInstanceUID: "study-1", TenantID: &tenantID, CandidateID: &candidateID,
			ProcessingRunID: &runID, ModelName: "EchoPrime", Status: "completed", CompletedAt: &completedAt,
		},
	}}
	metricsRecorder := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
		ProcessingReconciliationMetricsRecorder:   metricsRecorder,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{tenantID + ":" + jobID}, dispatcher.jobIDCalls)
	require.Empty(t, dispatcher.runCalls)
	require.Empty(t, dispatcher.calls)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusCompleted, runRepository.executions[runID][0].Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, runRepository.runs[0].Phase)
	require.False(t, runRepository.runs[0].AttentionRequired)
	require.Empty(t, runRepository.runs[0].AttentionReasons)
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Checked: 1, Repaired: 1}}, metricsRecorder.cycles)
}

func TestReconciliationWorkerRepairsMissedRunningCallback(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	startedAt := now.Add(-12 * time.Minute)
	runID := "run-1"
	candidateID := "candidate-1"
	tenantID := "tenant-a"
	jobID := "python-job-1"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: tenantID, StudyInstanceUID: "study-1",
			Phase: entity.InferenceIngestionProcessingRunPhaseQueued, Version: 2,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-1", ProcessingRunID: &runID, CandidateID: candidateID, TenantID: tenantID,
				ModelName: "EchoPrime", Status: entity.InferenceIngestionProcessingJobStatusQueued,
				StudyServiceJobID: &jobID, UpdatedAt: now.Add(-11 * time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		candidateID: {ID: candidateID, TenantID: tenantID, StudyInstanceUID: "study-1"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{jobsByID: map[string]serviceTypes.StudyServiceJob{
		jobID: {
			JobID: jobID, StudyInstanceUID: "study-1", TenantID: &tenantID, CandidateID: &candidateID,
			ProcessingRunID: &runID, ModelName: "EchoPrime", Status: "running", StartedAt: &startedAt,
		},
	}}
	metricsRecorder := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
		ProcessingReconciliationMetricsRecorder:   metricsRecorder,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusRunning, runRepository.executions[runID][0].Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseProcessing, runRepository.runs[0].Phase)
	require.Equal(t, startedAt, *runRepository.executions[runID][0].StartedAt)
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Checked: 1, Repaired: 1}}, metricsRecorder.cycles)
}

func TestReconciliationWorkerDoesNotRegressNewerGoState(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	runID := "run-1"
	candidateID := "candidate-1"
	tenantID := "tenant-a"
	jobID := "python-job-1"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: tenantID, StudyInstanceUID: "study-1",
			Phase: entity.InferenceIngestionProcessingRunPhaseProcessing, Version: 3,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-1", ProcessingRunID: &runID, CandidateID: candidateID, TenantID: tenantID,
				ModelName: "EchoPrime", Status: entity.InferenceIngestionProcessingJobStatusRunning,
				StudyServiceJobID: &jobID, UpdatedAt: now.Add(-70 * time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		candidateID: {ID: candidateID, TenantID: tenantID, StudyInstanceUID: "study-1"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{jobsByID: map[string]serviceTypes.StudyServiceJob{
		jobID: {
			JobID: jobID, StudyInstanceUID: "study-1", TenantID: &tenantID, CandidateID: &candidateID,
			ProcessingRunID: &runID, ModelName: "EchoPrime", Status: "queued",
		},
	}}
	metricsRecorder := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
		ProcessingReconciliationMetricsRecorder:   metricsRecorder,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusRunning, runRepository.executions[runID][0].Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseProcessing, runRepository.runs[0].Phase)
	require.True(t, runRepository.runs[0].AttentionRequired)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionProcessingStale,
		runRepository.runs[0].AttentionReasons[0].Code)
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Checked: 1, Unresolved: 1}}, metricsRecorder.cycles)
}

func TestReconciliationWorkerRepairsMissedCallbackByProcessingRunBeforeCandidateFallback(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	completedAt := now.Add(-time.Minute)
	runID := "run-1"
	candidateID := "candidate-1"
	tenantID := "tenant-a"
	jobID := "python-job-1"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: tenantID, StudyInstanceUID: "study-1",
			Phase: entity.InferenceIngestionProcessingRunPhaseProcessing, Version: 3,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-1", ProcessingRunID: &runID, CandidateID: candidateID, TenantID: tenantID,
				ModelName: "EchoPrime", Status: entity.InferenceIngestionProcessingJobStatusRunning,
				UpdatedAt: now.Add(-70 * time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		candidateID: {ID: candidateID, TenantID: tenantID, StudyInstanceUID: "study-1"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{jobsByRunID: map[string][]serviceTypes.StudyServiceJob{
		runID: {{
			JobID: jobID, StudyInstanceUID: "study-1", TenantID: &tenantID, CandidateID: &candidateID,
			ProcessingRunID: &runID, ModelName: "EchoPrime", Status: "completed", CompletedAt: &completedAt,
		}},
	}}
	metricsRecorder := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
		ProcessingReconciliationMetricsRecorder:   metricsRecorder,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Empty(t, dispatcher.jobIDCalls)
	require.Equal(t, []string{tenantID + ":" + runID}, dispatcher.runCalls)
	require.Empty(t, dispatcher.calls)
	// Run-level discovery avoids the candidate dispatcher fallback, while the
	// normal callback handler still loads the candidate to validate scope.
	require.Equal(t, []string{candidateID}, queryRepository.calls)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusCompleted, runRepository.executions[runID][0].Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, runRepository.runs[0].Phase)
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Checked: 1, Repaired: 1}}, metricsRecorder.cycles)
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
	metricsRecorder := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
		ProcessingReconciliationMetricsRecorder:   metricsRecorder,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"tenant-a:run-stale"}, dispatcher.runCalls)
	require.Len(t, runRepository.attempts, 1)
	require.False(t, runRepository.attempts[0].Succeeded)
	require.Equal(t, 3, runRepository.runs[0].ReconciliationFailureCount)
	require.Len(t, runRepository.aggregateUpdates, 1)
	require.Contains(t, runRepository.aggregateUpdates[0].AttentionReasons,
		entity.InferenceIngestionProcessingRunAttentionReason{
			Code: entity.InferenceIngestionProcessingRunAttentionReconciliationFailed,
		},
	)
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Checked: 1, Failed: 1}}, metricsRecorder.cycles)
}

func TestReconciliationWorkerDistinguishesExpectedJobAndDeadLetterWarnings(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	runID := "run-stale"
	candidateID := "candidate-stale"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-stale", CandidateID: candidateID, ModelName: "EchoPrime",
				Status: entity.InferenceIngestionProcessingJobStatusPending, UpdatedAt: now.Add(-3 * time.Minute),
			}},
		},
	}
	queryRepository := &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{
		candidateID: {ID: candidateID, TenantID: "tenant-a"},
	}}
	dispatcher := &reconciliationWorkerDispatcher{deadLetters: []serviceTypes.StudyServiceCallbackDeadLetter{{
		DeadLetterID: "dead-letter-1", JobID: "python-job-1",
		Payload: serviceTypes.StudyServiceCallbackDeadLetterPayload{
			CandidateID: candidateID, ProcessingRunID: runID, ModelName: "EchoPrime",
		},
	}}}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQueryRepositoryInterface:         queryRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Len(t, runRepository.aggregateUpdates, 1)
	require.Equal(t, []string{
		entity.InferenceIngestionProcessingRunAttentionPendingStale,
		entity.InferenceIngestionProcessingRunAttentionExpectedJobMissing,
		entity.InferenceIngestionProcessingRunAttentionCallbackDeadLettered,
	}, []string{
		runRepository.aggregateUpdates[0].AttentionReasons[0].Code,
		runRepository.aggregateUpdates[0].AttentionReasons[1].Code,
		runRepository.aggregateUpdates[0].AttentionReasons[2].Code,
	})
}

func TestReconciliationWorkerMarksCorrelationMismatchAsStateConflict(t *testing.T) {
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	now := time.Now()
	runID := "run-stale"
	candidateID := "candidate-stale"
	jobID := "python-job-1"
	runRepository := &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: "tenant-a", Phase: entity.InferenceIngestionProcessingRunPhaseProcessing,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-stale", CandidateID: candidateID, ModelName: "EchoPrime",
				StudyServiceJobID: &jobID, Status: entity.InferenceIngestionProcessingJobStatusRunning,
				UpdatedAt: now.Add(-70 * time.Minute),
			}},
		},
	}
	dispatcher := &reconciliationWorkerDispatcher{jobsByID: map[string]serviceTypes.StudyServiceJob{
		jobID: {
			JobID: jobID, ProcessingRunID: stringPointer("other-run"), CandidateID: &candidateID,
			ModelName: "EchoPrime", Status: "running",
		},
	}}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.ExecuteInferenceIngestionReconciliationWorker(context.Background())

	require.NoError(t, err)
	require.Len(t, runRepository.aggregateUpdates, 1)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionStateConflict,
		runRepository.aggregateUpdates[0].AttentionReasons[0].Code)
	require.NotNil(t, runRepository.aggregateUpdates[0].AttentionReasons[0].Message)
	require.Contains(t, *runRepository.aggregateUpdates[0].AttentionReasons[0].Message, "processing-run mismatch")
	require.Len(t, runRepository.attempts, 1)
	require.False(t, runRepository.attempts[0].Succeeded)
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

func TestValidateReconciledManualJobRequiresExactExecutionIdentity(t *testing.T) {
	runID := "run-1"
	executionID := "execution-1"
	otherExecutionID := "execution-other"
	run := entity.InferenceIngestionProcessingRun{
		ID: runID, TenantID: "tenant-a",
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
	}
	execution := entity.InferenceIngestionProcessingJob{
		ID: executionID, CandidateID: "candidate-1", ModelName: "EchoPrime",
	}
	job := serviceTypes.StudyServiceJob{
		JobID: "job-1", ProcessingRunID: &runID, ProcessingExecutionID: &executionID,
		ModelName: "EchoPrime",
	}

	require.NoError(t, validateReconciledStudyServiceJob(run, execution, job))
	job.ProcessingExecutionID = nil
	require.ErrorContains(t, validateReconciledStudyServiceJob(run, execution, job), "unbound manual job")
	execution.StudyServiceJobID = &job.JobID
	require.NoError(t, validateReconciledStudyServiceJob(run, execution, job))
	job.ProcessingExecutionID = &otherExecutionID
	require.ErrorContains(t, validateReconciledStudyServiceJob(run, execution, job), "processing-execution mismatch")

	run.RunTrigger = entity.InferenceIngestionProcessingRunTriggerAuto
	require.NoError(t, validateReconciledStudyServiceJob(run, execution, job))
}
