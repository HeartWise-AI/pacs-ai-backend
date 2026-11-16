package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// cancelRunRepository keeps one run in memory and applies execution
// transitions with the domain state machine, like the PostgreSQL repository.
type cancelRunRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	run         *entity.InferenceIngestionProcessingRun
	executions  []entity.InferenceIngestionProcessingJob
	transitions []repositoryTypes.ApplyInferenceIngestionProcessingTransition
	marks       []repositoryTypes.MarkInferenceIngestionProcessingRunCancelled
	created     []repositoryTypes.CreateInferenceIngestionProcessingRunPlan
	// beforeTransition lets a test change state between the read and the lock.
	beforeTransition func(*cancelRunRepository)
}

func (repository *cancelRunRepository) ownsRun(tenantID, runID string) bool {
	return repository.run != nil && repository.run.TenantID == tenantID && repository.run.ID == runID
}

func (repository *cancelRunRepository) SelectProcessingRun(_ context.Context, tenantID, runID string) (entity.InferenceIngestionProcessingRun, error) {
	if !repository.ownsRun(tenantID, runID) {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return *repository.run, nil
}

func (repository *cancelRunRepository) ListProcessingRunExecutions(_ context.Context, tenantID, runID string) ([]entity.InferenceIngestionProcessingJob, error) {
	if !repository.ownsRun(tenantID, runID) {
		return nil, nil
	}
	return append([]entity.InferenceIngestionProcessingJob(nil), repository.executions...), nil
}

func (repository *cancelRunRepository) SelectProcessingRunExecution(_ context.Context, _, _, candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error) {
	for _, execution := range repository.executions {
		if execution.CandidateID == candidateID && execution.ModelName == modelName {
			return execution, nil
		}
	}
	return entity.InferenceIngestionProcessingJob{}, errors.New(apiError.MissingRecord)
}

func (repository *cancelRunRepository) SelectActiveProcessingRun(_ context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	if repository.run == nil || repository.run.TenantID != tenantID || repository.run.StudyInstanceUID != studyInstanceUID ||
		repository.run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return *repository.run, nil
}

func (repository *cancelRunRepository) SelectLatestStudyWideProcessingRun(_ context.Context, tenantID, studyInstanceUID string) (entity.InferenceIngestionProcessingRun, error) {
	if repository.run == nil || repository.run.TenantID != tenantID || repository.run.StudyInstanceUID != studyInstanceUID {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return *repository.run, nil
}

func (repository *cancelRunRepository) MarkProcessingRunCancelled(_ context.Context, data repositoryTypes.MarkInferenceIngestionProcessingRunCancelled) (entity.InferenceIngestionProcessingRun, error) {
	if !repository.ownsRun(data.TenantID, data.ID) || repository.run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	repository.marks = append(repository.marks, data)
	if repository.run.CancelledAt == nil {
		userID, at := data.CancelledByUserID, data.CancelledAt
		repository.run.CancelledByUserID, repository.run.CancelledAt = &userID, &at
	}
	return *repository.run, nil
}

func (repository *cancelRunRepository) applyAggregate() entity.InferenceIngestionProcessingRunExecutionCounts {
	aggregate := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
		Run: *repository.run, Executions: repository.executions,
	})
	repository.run.Phase = aggregate.Phase
	repository.run.Outcome = aggregate.Outcome
	repository.run.AttentionRequired = aggregate.AttentionRequired
	repository.run.AttentionReasons = aggregate.AttentionReasons
	repository.run.StartedAt = aggregate.StartedAt
	repository.run.CompletedAt = aggregate.CompletedAt
	repository.run.Version++
	return aggregate.Counts
}

func (repository *cancelRunRepository) ApplyProcessingRunExecutionTransition(
	_ context.Context,
	data repositoryTypes.ApplyInferenceIngestionProcessingTransition,
) (repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult, error) {
	if repository.beforeTransition != nil {
		repository.beforeTransition(repository)
	}
	repository.transitions = append(repository.transitions, data)
	for index := range repository.executions {
		execution := &repository.executions[index]
		if execution.ID != data.ExecutionID {
			continue
		}
		ignored := repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{
			Outcome: "ignored", Execution: *execution, Run: *repository.run,
		}
		if !execution.Status.CanTransitionTo(data.Status) {
			return ignored, nil
		}
		if data.OnlyIfCancellableBeforeCompute && !execution.IsCancellableBeforeCompute(data.StudyServiceJobMissing) {
			return ignored, nil
		}
		execution.Status = data.Status
		execution.ErrorMessage = data.ErrorMessage
		if data.StartedAt != nil {
			execution.StartedAt = data.StartedAt
		}
		if data.CompletedAt != nil {
			execution.CompletedAt = data.CompletedAt
		}
		counts := repository.applyAggregate()
		return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{
			Outcome: "applied", Changed: true, Execution: *execution, Run: *repository.run, Counts: counts,
		}, nil
	}
	return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{}, errors.New(apiError.MissingRecord)
}

func (repository *cancelRunRepository) UpdateProcessingRunAggregate(_ context.Context, data repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error) {
	if data.ExpectedVersion != repository.run.Version {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.DuplicateRecord)
	}
	repository.run.Phase = data.Phase
	repository.run.Outcome = data.Outcome
	repository.run.AttentionRequired = data.AttentionRequired
	repository.run.AttentionReasons = data.AttentionReasons
	repository.run.StartedAt = data.StartedAt
	repository.run.CompletedAt = data.CompletedAt
	repository.run.Version++
	return *repository.run, nil
}

func (repository *cancelRunRepository) CreateProcessingRunPlan(_ context.Context, data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
	repository.created = append(repository.created, data)
	return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{
		Run:     entity.InferenceIngestionProcessingRun{ID: data.Run.ID, RunTrigger: data.Run.RunTrigger},
		Created: true,
	}, nil
}

type recordingQuotaManager struct {
	allowAllInferenceQuotaManager
	refunded []serviceTypes.InferenceQuotaReservation
	released []serviceTypes.InferenceQuotaReservation
}

func (manager *recordingQuotaManager) Refund(_ context.Context, reservation serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	manager.refunded = append(manager.refunded, reservation)
	return serviceTypes.InferenceQuotaStatus{}, nil
}

func (manager *recordingQuotaManager) Release(_ context.Context, reservation serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	manager.released = append(manager.released, reservation)
	return serviceTypes.InferenceQuotaStatus{}, nil
}

func cancelExecution(id string, status entity.InferenceIngestionProcessingJobStatus, studyServiceJobID string) entity.InferenceIngestionProcessingJob {
	runID := "run-1"
	execution := entity.InferenceIngestionProcessingJob{
		ID: id, ProcessingRunID: &runID, CandidateID: "candidate-" + id, TenantID: "tenant-a",
		ModelName: "model-" + id, Status: status,
	}
	if studyServiceJobID != "" {
		execution.StudyServiceJobID = &studyServiceJobID
	}
	if status == entity.InferenceIngestionProcessingJobStatusCompleted {
		completedAt := time.Date(2026, time.November, 16, 9, 0, 0, 0, time.UTC)
		execution.StartedAt, execution.CompletedAt = &completedAt, &completedAt
	}
	return execution
}

func cancelFixture(
	trigger entity.InferenceIngestionProcessingRunTrigger,
	executions ...entity.InferenceIngestionProcessingJob,
) (*InferenceCommandService, *cancelRunRepository, *recordingWorklistNotificationPublisher) {
	run := entity.InferenceIngestionProcessingRun{
		ID: "run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunNumber: 1, RunTrigger: trigger,
		Phase: entity.InferenceIngestionProcessingRunPhaseQueued, Version: 1,
	}
	repository := &cancelRunRepository{run: &run, executions: executions}
	// Start from the state the aggregate would have persisted.
	repository.applyAggregate()
	publisher := &recordingWorklistNotificationPublisher{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: repository,
		WorklistNotificationPublisherInterface:    publisher,
	}
	return service, repository, publisher
}

func cancelRun(service *InferenceCommandService) (serviceTypes.ProcessingRunDetail, error) {
	return service.CancelProcessingRun(context.Background(), serviceTypes.CancelProcessingRun{
		TenantID: " tenant-a ", RunID: " run-1 ", UserID: " user-a ",
	})
}

func executionStatuses(executions []entity.InferenceIngestionProcessingJob) map[string]entity.InferenceIngestionProcessingJobStatus {
	statuses := make(map[string]entity.InferenceIngestionProcessingJobStatus, len(executions))
	for _, execution := range executions {
		statuses[execution.ID] = execution.Status
	}
	return statuses
}

func TestCancelProcessingRunCancelsWaitingModelsOfAPartialRun(t *testing.T) {
	service, repository, publisher := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("a", entity.InferenceIngestionProcessingJobStatusCompleted, "job-a"),
		cancelExecution("b", entity.InferenceIngestionProcessingJobStatusCompleted, "job-b"),
		cancelExecution("c", entity.InferenceIngestionProcessingJobStatusCompleted, "job-c"),
		cancelExecution("d", entity.InferenceIngestionProcessingJobStatusPending, ""),
		cancelExecution("e", entity.InferenceIngestionProcessingJobStatusPending, ""),
		cancelExecution("f", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)
	repository.run.AttentionRequired = true
	repository.run.AttentionReasons = entity.InferenceIngestionProcessingRunAttentionReasons{
		{Code: entity.InferenceIngestionProcessingRunAttentionPendingStale},
		{Code: entity.InferenceIngestionProcessingRunAttentionExpectedJobMissing},
	}

	detail, err := cancelRun(service)

	require.NoError(t, err)
	require.Equal(t, map[string]entity.InferenceIngestionProcessingJobStatus{
		"a": entity.InferenceIngestionProcessingJobStatusCompleted,
		"b": entity.InferenceIngestionProcessingJobStatusCompleted,
		"c": entity.InferenceIngestionProcessingJobStatusCompleted,
		"d": entity.InferenceIngestionProcessingJobStatusCancelled,
		"e": entity.InferenceIngestionProcessingJobStatusCancelled,
		"f": entity.InferenceIngestionProcessingJobStatusCancelled,
	}, executionStatuses(repository.executions))

	// The run reached its terminal phase through the ordinary transition.
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, detail.Phase)
	require.NotNil(t, detail.Outcome)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *detail.Outcome)
	require.Equal(t, 3, detail.Completed)
	require.Equal(t, 3, detail.Cancelled)
	require.Zero(t, detail.Active)
	require.False(t, detail.Cancellable)
	require.NotNil(t, detail.CancelledAt)
	require.Empty(t, detail.WaitingReason)
	// The stale flags only concerned the cancelled executions.
	require.False(t, detail.AttentionRequired)
	require.Empty(t, detail.AttentionReasons)

	// Who and when.
	require.Len(t, repository.marks, 1)
	require.Equal(t, "user-a", repository.marks[0].CancelledByUserID)
	require.Equal(t, "user-a", *repository.run.CancelledByUserID)
	require.WithinDuration(t, time.Now(), *repository.run.CancelledAt, time.Minute)

	require.Len(t, repository.transitions, 3)
	for _, transition := range repository.transitions {
		require.Equal(t, entity.InferenceIngestionProcessingJobStatusCancelled, transition.Status)
		require.True(t, transition.OnlyIfCancellableBeforeCompute)
		require.Equal(t, "Cancelled by user", *transition.ErrorMessage)
		require.NotNil(t, transition.CompletedAt)
		require.Nil(t, transition.EventID)
	}

	// One committed worklist event per cancelled execution; the last is terminal.
	require.Len(t, publisher.notifications, 3)
	last := publisher.notifications[2]
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, last.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *last.Outcome)
	require.Equal(t, 3, last.CancelledModels)
	require.Equal(t, 3, last.CompletedModels)
	require.False(t, last.AttentionRequired)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusCancelled, last.Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseQueued, publisher.notifications[0].Phase)
}

func TestCancelProcessingRunWithoutCompletedModelEndsCancelledAndRefundsQuota(t *testing.T) {
	service, repository, publisher := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerViewerRequest,
		cancelExecution("a", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)
	userID := "user-a"
	repository.run.RequestedByUserID = &userID
	quota := &recordingQuotaManager{}
	service.InferenceQuotaManagerInterface = quota

	detail, err := cancelRun(service)

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, detail.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomeCancelled, *detail.Outcome)
	require.Equal(t, 1, detail.Cancelled)
	require.Len(t, publisher.notifications, 1)
	// Nothing was computed: the reserved unit goes back to the requester.
	require.Len(t, quota.refunded, 1)
	require.Equal(t, "run-1", quota.refunded[0].ReservationID)
	require.Empty(t, quota.released)
}

func TestCancelProcessingRunLetsARunningModelFinishAndRecordItsResult(t *testing.T) {
	service, repository, _ := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("running", entity.InferenceIngestionProcessingJobStatusRunning, "job-running"),
		cancelExecution("dispatched", entity.InferenceIngestionProcessingJobStatusQueued, "job-dispatched"),
		cancelExecution("undispatched", entity.InferenceIngestionProcessingJobStatusQueued, ""),
		cancelExecution("pending", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)

	detail, err := cancelRun(service)

	require.NoError(t, err)
	require.Equal(t, map[string]entity.InferenceIngestionProcessingJobStatus{
		// Handed to study-service: not interrupted.
		"running":      entity.InferenceIngestionProcessingJobStatusRunning,
		"dispatched":   entity.InferenceIngestionProcessingJobStatusQueued,
		"undispatched": entity.InferenceIngestionProcessingJobStatusCancelled,
		"pending":      entity.InferenceIngestionProcessingJobStatusCancelled,
	}, executionStatuses(repository.executions))
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseProcessing, detail.Phase)
	require.Nil(t, detail.Outcome)
	require.Equal(t, 2, detail.Active)
	require.False(t, detail.Cancellable)
	require.NotNil(t, detail.CancelledAt)

	// The models left to finish report through the same transition and the
	// run then ends with their results kept.
	completedAt := time.Now().UTC()
	for _, id := range []string{"running", "dispatched"} {
		_, err = repository.ApplyProcessingRunExecutionTransition(context.Background(), repositoryTypes.ApplyInferenceIngestionProcessingTransition{
			TenantID: "tenant-a", ProcessingRunID: "run-1", ExecutionID: id,
			Status: entity.InferenceIngestionProcessingJobStatusCompleted, CompletedAt: &completedAt,
		})
		require.NoError(t, err)
	}
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, repository.run.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *repository.run.Outcome)

	// Repeating the request on the ended run changes nothing.
	transitions := len(repository.transitions)
	again, err := cancelRun(service)
	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, again.Phase)
	require.Len(t, repository.transitions, transitions)
	require.Len(t, repository.marks, 1)
}

func TestCancelProcessingRunCancelsAQueuedModelStudyServiceLost(t *testing.T) {
	service, repository, _ := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("done", entity.InferenceIngestionProcessingJobStatusCompleted, "job-done"),
		cancelExecution("lost", entity.InferenceIngestionProcessingJobStatusQueued, "job-lost"),
	)
	repository.run.AttentionRequired = true
	repository.run.AttentionReasons = entity.InferenceIngestionProcessingRunAttentionReasons{
		{Code: entity.InferenceIngestionProcessingRunAttentionQueueStale},
		{Code: entity.InferenceIngestionProcessingRunAttentionStudyServiceJobMissing},
	}

	detail, err := cancelRun(service)

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusCancelled, repository.executions[1].Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, detail.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *detail.Outcome)
	require.False(t, detail.AttentionRequired)
}

func TestCancelProcessingRunRejectsARunThatIsOnlyComputing(t *testing.T) {
	service, repository, publisher := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerViewerRequest,
		cancelExecution("running", entity.InferenceIngestionProcessingJobStatusRunning, "job-running"),
	)

	_, err := cancelRun(service)

	require.EqualError(t, err, apiError.ProcessingRunNotCancellable)
	require.Empty(t, repository.marks)
	require.Empty(t, repository.transitions)
	require.Empty(t, publisher.notifications)
	require.Nil(t, repository.run.CancelledAt)
}

func TestCancelProcessingRunIsIdempotent(t *testing.T) {
	t.Run("already cancelled run with a model still running", func(t *testing.T) {
		service, repository, publisher := cancelFixture(
			entity.InferenceIngestionProcessingRunTriggerAuto,
			cancelExecution("running", entity.InferenceIngestionProcessingJobStatusRunning, "job-running"),
			cancelExecution("pending", entity.InferenceIngestionProcessingJobStatusPending, ""),
		)
		first, err := cancelRun(service)
		require.NoError(t, err)
		firstCancelledAt := *repository.run.CancelledAt

		second, err := cancelRun(service)

		require.NoError(t, err)
		require.Equal(t, first.Phase, second.Phase)
		require.Equal(t, first.Version, second.Version)
		require.Len(t, repository.marks, 1)
		require.Len(t, repository.transitions, 1)
		require.Len(t, publisher.notifications, 1)
		require.Equal(t, firstCancelledAt, *repository.run.CancelledAt)
	})

	t.Run("run that ended on its own", func(t *testing.T) {
		service, repository, publisher := cancelFixture(
			entity.InferenceIngestionProcessingRunTriggerAuto,
			cancelExecution("a", entity.InferenceIngestionProcessingJobStatusCompleted, "job-a"),
		)

		detail, err := cancelRun(service)

		require.NoError(t, err)
		require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, detail.Phase)
		require.Equal(t, entity.InferenceIngestionProcessingRunOutcomeSuccess, *detail.Outcome)
		require.Nil(t, detail.CancelledAt)
		require.Empty(t, repository.marks)
		require.Empty(t, repository.transitions)
		require.Empty(t, publisher.notifications)
	})
}

func TestCancelProcessingRunIsTenantScoped(t *testing.T) {
	service, repository, _ := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("pending", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)

	for name, input := range map[string]serviceTypes.CancelProcessingRun{
		"unknown run":  {TenantID: "tenant-a", RunID: "run-missing", UserID: "user-a"},
		"other tenant": {TenantID: "tenant-b", RunID: "run-1", UserID: "user-b"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.CancelProcessingRun(context.Background(), input)
			require.EqualError(t, err, apiError.MissingRecord)
		})
	}
	for name, input := range map[string]serviceTypes.CancelProcessingRun{
		"missing tenant": {RunID: "run-1", UserID: "user-a"},
		"missing run":    {TenantID: "tenant-a", UserID: "user-a"},
		"missing user":   {TenantID: "tenant-a", RunID: "run-1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.CancelProcessingRun(context.Background(), input)
			require.EqualError(t, err, apiError.InvalidPayload)
		})
	}
	require.Empty(t, repository.marks)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusPending, repository.executions[0].Status)
}

func TestCancelProcessingRunLeavesAModelDispatchedMeanwhile(t *testing.T) {
	service, repository, publisher := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("racing", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)
	// The dispatcher hands the execution to study-service after the
	// cancellation read it as pending, before the transition locks it.
	repository.beforeTransition = func(repository *cancelRunRepository) {
		jobID := "job-racing"
		repository.executions[0].Status = entity.InferenceIngestionProcessingJobStatusQueued
		repository.executions[0].StudyServiceJobID = &jobID
	}

	detail, err := cancelRun(service)

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusQueued, repository.executions[0].Status)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseQueued, detail.Phase)
	require.Empty(t, publisher.notifications)
}

func TestAutomaticIngestionDoesNotRecreateAUserCancelledRun(t *testing.T) {
	service, repository, _ := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("done", entity.InferenceIngestionProcessingJobStatusCompleted, "job-done"),
		cancelExecution("growing", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)
	candidate := entity.InferenceIngestionCandidate{
		ID: "candidate-growing", TenantID: "tenant-a", IngestionJobID: "ingestion-job-growing",
		StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved,
	}
	ingestionJob := entity.InferenceIngestionJob{
		ID: "ingestion-job-growing", TenantID: "tenant-a", ModelName: "model-growing", DICOMModality: "XA",
	}
	service.InferenceQueryRepositoryInterface = &processingRunPlannerQueryRepository{
		candidates: []entity.InferenceIngestionCandidate{candidate},
		jobs:       map[string]entity.InferenceIngestionJob{ingestionJob.ID: ingestionJob},
	}
	commandRepository := &cancelCandidateCommandRepository{}
	service.InferenceCommandRepositoryInterface = commandRepository
	planInput := serviceTypes.CreateStudyProcessingRun{TenantID: "tenant-a", StudyInstanceUID: "1.2.3"}

	_, err := cancelRun(service)
	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, repository.run.Phase)

	// The study's remaining candidate later becomes stable and is retrieved.
	_, err = service.CreateAutomaticStudyProcessingRun(context.Background(), planInput)
	require.ErrorIs(t, err, errAutomaticProcessingSuppressedByCancellation)
	require.Empty(t, repository.created)

	// The retrieval worker treats the suppression as handled, not as a failure.
	err = service.persistCandidateRetrievalResult(context.Background(), ingestionJob, candidate, candidateRetrievalResult{
		Outcome: candidateRetrievalOutcomeLocal,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"candidate-growing"}, commandRepository.retrieved)
	require.Empty(t, repository.created)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusCancelled, repository.executions[1].Status)
}

func TestAutomaticIngestionDoesNotDispatchCancelledModelsOfAStillActiveRun(t *testing.T) {
	service, repository, _ := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("running", entity.InferenceIngestionProcessingJobStatusRunning, "job-running"),
		cancelExecution("growing", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)
	_, err := cancelRun(service)
	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseProcessing, repository.run.Phase)

	// The active run is reused, not replaced...
	plan, err := service.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})
	require.NoError(t, err)
	require.False(t, plan.Created)
	require.Equal(t, "run-1", plan.Run.ID)
	require.Empty(t, repository.created)

	// ...and its cancelled execution is no longer eligible for dispatch.
	shouldDispatch, _, err := service.shouldDispatchCommittedProcessingExecution(
		context.Background(),
		entity.InferenceIngestionCandidate{ID: "candidate-growing", TenantID: "tenant-a"},
		entity.InferenceIngestionJob{ModelName: "model-growing"},
		"run-1", "", serviceTypes.DispatchStudyIntentAutomatic,
	)
	require.NoError(t, err)
	require.False(t, shouldDispatch)
}

func TestManualReprocessStillWorksAfterAUserCancellation(t *testing.T) {
	service, repository, _ := cancelFixture(
		entity.InferenceIngestionProcessingRunTriggerAuto,
		cancelExecution("pending", entity.InferenceIngestionProcessingJobStatusPending, ""),
	)
	_, err := cancelRun(service)
	require.NoError(t, err)

	// A manual plan does not consult the cancellation; it fails later, on the
	// missing user, only after passing the point where automatic plans stop.
	service.InferenceQueryRepositoryInterface = &processingRunPlannerQueryRepository{}
	_, err = service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})
	require.NotErrorIs(t, err, errAutomaticProcessingSuppressedByCancellation)
	require.EqualError(t, err, apiError.MissingRecord)
	require.Empty(t, repository.created)
}

type cancelCandidateCommandRepository struct {
	domainRepository.InferenceCommandRepositoryInterface
	retrieved []string
}

func (repository *cancelCandidateCommandRepository) MarkCandidateRetrievedWithContext(data repositoryTypes.UpdateCandidateRetrievalState) error {
	repository.retrieved = append(repository.retrieved, data.ID)
	return nil
}

func TestUserCancelledRunOutcome(t *testing.T) {
	cancelledAt := time.Date(2026, time.November, 16, 10, 0, 0, 0, time.UTC)
	statuses := func(values ...entity.InferenceIngestionProcessingJobStatus) []entity.InferenceIngestionProcessingJob {
		executions := make([]entity.InferenceIngestionProcessingJob, 0, len(values))
		for _, status := range values {
			executions = append(executions, entity.InferenceIngestionProcessingJob{Status: status})
		}
		return executions
	}
	const (
		completed = entity.InferenceIngestionProcessingJobStatusCompleted
		cancelled = entity.InferenceIngestionProcessingJobStatusCancelled
		failed    = entity.InferenceIngestionProcessingJobStatusFailed
		skipped   = entity.InferenceIngestionProcessingJobStatusSkipped
		running   = entity.InferenceIngestionProcessingJobStatusRunning
	)

	for _, test := range []struct {
		name          string
		userCancelled bool
		executions    []entity.InferenceIngestionProcessingJob
		phase         entity.InferenceIngestionProcessingRunPhase
		outcome       entity.InferenceIngestionProcessingRunOutcome
	}{
		{"some completed, rest cancelled", true, statuses(completed, completed, cancelled), entity.InferenceIngestionProcessingRunPhaseTerminal, entity.InferenceIngestionProcessingRunOutcomePartialSuccess},
		{"all cancelled", true, statuses(cancelled, cancelled), entity.InferenceIngestionProcessingRunPhaseTerminal, entity.InferenceIngestionProcessingRunOutcomeCancelled},
		{"cancelled with failed and skipped, none completed", true, statuses(cancelled, failed, skipped), entity.InferenceIngestionProcessingRunPhaseTerminal, entity.InferenceIngestionProcessingRunOutcomeCancelled},
		{"cancelled while one still runs", true, statuses(cancelled, running), entity.InferenceIngestionProcessingRunPhaseProcessing, ""},
		{"cancel requested but everything completed", true, statuses(completed, completed), entity.InferenceIngestionProcessingRunPhaseTerminal, entity.InferenceIngestionProcessingRunOutcomeSuccess},
		{"study-service cancellation without a user keeps the earlier rule", false, statuses(cancelled, failed), entity.InferenceIngestionProcessingRunPhaseTerminal, entity.InferenceIngestionProcessingRunOutcomeFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := entity.InferenceIngestionProcessingRun{ID: "run-1"}
			if test.userCancelled {
				run.CancelledAt = &cancelledAt
			}

			aggregate := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
				Run: run, Executions: test.executions,
			})

			require.Equal(t, test.phase, aggregate.Phase)
			if test.outcome == "" {
				require.Nil(t, aggregate.Outcome)
				return
			}
			require.NotNil(t, aggregate.Outcome)
			require.Equal(t, test.outcome, *aggregate.Outcome)
		})
	}
}
