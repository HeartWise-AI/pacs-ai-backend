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

type processingRunCallbackQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	candidate entity.InferenceIngestionCandidate
	execution entity.InferenceIngestionProcessingJob
}

func (repository *processingRunCallbackQueryRepository) SelectInferenceIngestionCandidateByID(string) (entity.InferenceIngestionCandidate, error) {
	return repository.candidate, nil
}

func (repository *processingRunCallbackQueryRepository) SelectInferenceIngestionProcessingJobByCandidateModel(string, string) (entity.InferenceIngestionProcessingJob, error) {
	return repository.execution, nil
}

type processingRunCallbackCommandRepository struct {
	domainRepository.InferenceCommandRepositoryInterface
	updates []repositoryTypes.UpdateInferenceIngestionProcessingJob
}

type processingRunCallbackRunRepository struct {
	*processingRunAggregationRepository
	selectedExecution entity.InferenceIngestionProcessingJob
	transitions       []repositoryTypes.ApplyInferenceIngestionProcessingTransition
	transitionResult  *repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult
	transitionErr     error
}

func (repository *processingRunCallbackRunRepository) SelectProcessingRunExecution(context.Context, string, string, string, string) (entity.InferenceIngestionProcessingJob, error) {
	return repository.selectedExecution, nil
}

func (repository *processingRunCallbackRunRepository) ApplyProcessingRunExecutionTransition(
	_ context.Context,
	data repositoryTypes.ApplyInferenceIngestionProcessingTransition,
) (repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult, error) {
	repository.transitions = append(repository.transitions, data)
	if repository.transitionErr != nil {
		return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{}, repository.transitionErr
	}
	if repository.transitionResult != nil {
		return *repository.transitionResult, nil
	}
	outcome := "applied"
	if data.EventID == nil && repository.selectedExecution.Status == data.Status {
		outcome = "replayed"
	}
	return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{Outcome: outcome}, nil
}

type recordingWorklistNotificationPublisher struct {
	notifications []serviceTypes.WorklistNotification
	err           error
}

func (publisher *recordingWorklistNotificationPublisher) PublishWorklistNotification(
	_ context.Context,
	notification serviceTypes.WorklistNotification,
) error {
	publisher.notifications = append(publisher.notifications, notification)
	return publisher.err
}

func (repository *processingRunCallbackCommandRepository) UpdateInferenceIngestionProcessingJob(data repositoryTypes.UpdateInferenceIngestionProcessingJob) error {
	repository.updates = append(repository.updates, data)
	return nil
}

func orderedProcessingCallbackFixture(
	status entity.InferenceIngestionProcessingJobStatus,
	lastEventID *string,
	lastEventSequence *int64,
) (*InferenceCommandService, *processingRunCallbackCommandRepository, *processingRunCallbackRunRepository) {
	processingRunID := "run-1"
	execution := entity.InferenceIngestionProcessingJob{
		ID: "execution-1", ProcessingRunID: &processingRunID, CandidateID: "candidate-1",
		TenantID: "tenant-a", ModelName: "model-one", Status: status,
		LastEventID: lastEventID, LastEventSequence: lastEventSequence,
	}
	queryRepository := &processingRunCallbackQueryRepository{
		candidate: entity.InferenceIngestionCandidate{
			ID: "candidate-1", TenantID: "tenant-a", StudyInstanceUID: "study-1",
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: execution,
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{
				ID: processingRunID, TenantID: "tenant-a", StudyInstanceUID: "study-1", Version: 1,
			}},
			executions: []entity.InferenceIngestionProcessingJob{execution},
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}
	return service, commandRepository, runRepository
}

func TestProcessingCallbackRecalculatesLinkedRunAfterAppliedTransition(t *testing.T) {
	processingRunID := "run-1"
	queryRepository := &processingRunCallbackQueryRepository{
		candidate: entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a", StudyInstanceUID: "study-1"},
		execution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, CandidateID: "candidate-1",
			TenantID: "tenant-a", ModelName: "model-one", Status: entity.InferenceIngestionProcessingJobStatusQueued,
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: queryRepository.execution,
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{
				ID: processingRunID, TenantID: "tenant-a", StudyInstanceUID: "study-1", Version: 2,
			}},
			executions: []entity.InferenceIngestionProcessingJob{
				{ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusRunning},
			},
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: processingRunID, StudyInstanceUID: "study-1", ModelName: "model-one", Status: "running",
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Len(t, runRepository.transitions, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusRunning, runRepository.transitions[0].Status)
}

func TestProcessingCallbackPassesStructuredSkipReasonToAtomicTransition(t *testing.T) {
	service, commandRepository, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusRunning,
		nil,
		nil,
	)
	message := "No usable DICOM series"
	skipReason := &entity.InferenceIngestionProcessingJobSkipReason{
		Code: entity.InferenceIngestionProcessingJobSkipReasonNoUsableDICOM, Message: &message,
	}

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "skipped", SkipReason: skipReason,
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Len(t, runRepository.transitions, 1)
	require.Equal(t, skipReason, runRepository.transitions[0].SkipReason)
}

func TestProcessingCallbackPublishesCommittedRunNotification(t *testing.T) {
	service, _, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusQueued,
		nil,
		nil,
	)
	publisher := &recordingWorklistNotificationPublisher{}
	service.WorklistNotificationPublisherInterface = publisher
	updatedAt := time.Date(2026, time.August, 5, 14, 0, 0, 0, time.UTC)
	startedAt := updatedAt.Add(-time.Minute)
	attentionMessage := "callback delivery exhausted"
	runRepository.transitionResult = &repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{
		Outcome: "applied",
		Changed: true,
		Run: entity.InferenceIngestionProcessingRun{
			ID: "run-1", TenantID: "tenant-a", StudyInstanceUID: "study-1", RunNumber: 2,
			RunTrigger:        entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:             entity.InferenceIngestionProcessingRunPhaseProcessing,
			AttentionRequired: true,
			AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
				Code:    entity.InferenceIngestionProcessingRunAttentionCallbackDeadLettered,
				Message: &attentionMessage,
			}},
			Version: 7, StartedAt: &startedAt, UpdatedAt: updatedAt,
		},
		Counts: entity.InferenceIngestionProcessingRunExecutionCounts{
			Expected: 8, Pending: 1, Queued: 1, Running: 1, Completed: 1,
			Failed: 1, Skipped: 1, Cancelled: 1, Active: 3,
		},
	}

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "running",
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Len(t, publisher.notifications, 1)
	notification := publisher.notifications[0]
	require.Equal(t, serviceTypes.WorklistNotificationTypeStudyStatusUpdated, notification.Type)
	require.Equal(t, "tenant-a", notification.TenantID)
	require.Equal(t, "study-1", notification.StudyInstanceUID)
	require.Equal(t, "run-1", notification.RunID)
	require.Equal(t, 2, notification.RunNumber)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerAuto, notification.Trigger)
	require.True(t, notification.AttentionRequired)
	require.Len(t, notification.AttentionReasons, 1)
	require.Equal(t, int64(7), notification.Version)
	require.Equal(t, 8, notification.ExpectedModels)
	require.Equal(t, 1, notification.PendingModels)
	require.Equal(t, 1, notification.QueuedModels)
	require.Equal(t, 1, notification.RunningModels)
	require.Equal(t, 1, notification.CompletedModels)
	require.Equal(t, 1, notification.FailedModels)
	require.Equal(t, 1, notification.SkippedModels)
	require.Equal(t, 1, notification.CancelledModels)
	require.Equal(t, 3, notification.ActiveModels)
	require.Equal(t, &startedAt, notification.StartedAt)
	require.Equal(t, updatedAt, notification.UpdatedAt)
}

func TestProcessingCallbackPublishesNothingWhenAtomicTransitionFails(t *testing.T) {
	service, _, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusQueued,
		nil,
		nil,
	)
	publisher := &recordingWorklistNotificationPublisher{}
	service.WorklistNotificationPublisherInterface = publisher
	runRepository.transitionErr = errors.New(apiError.DatabaseError)

	_, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "running",
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.Empty(t, publisher.notifications)
}

func TestProcessingCallbackReplayHealsLinkedRunAggregate(t *testing.T) {
	processingRunID := "run-1"
	queryRepository := &processingRunCallbackQueryRepository{
		candidate: entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a", StudyInstanceUID: "study-1"},
		execution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, CandidateID: "candidate-1",
			TenantID: "tenant-a", ModelName: "model-one", Status: entity.InferenceIngestionProcessingJobStatusRunning,
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: queryRepository.execution,
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{
				ID: processingRunID, TenantID: "tenant-a", StudyInstanceUID: "study-1", Version: 7,
			}},
			executions: []entity.InferenceIngestionProcessingJob{
				{ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusRunning},
			},
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: processingRunID, StudyInstanceUID: "study-1", ModelName: "model-one", Status: "running",
	})

	require.NoError(t, err)
	require.Equal(t, "replayed", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Len(t, runRepository.transitions, 1)
}

func TestProcessingCallbackTreatsDuplicateEventIDAsMutationFreeReplay(t *testing.T) {
	lastEventID := "event-running"
	lastEventSequence := int64(2)
	service, commandRepository, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusRunning,
		&lastEventID,
		&lastEventSequence,
	)

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "running", EventID: lastEventID, Sequence: &lastEventSequence,
	})

	require.NoError(t, err)
	require.Equal(t, "replayed", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Empty(t, runRepository.updates)
}

func TestProcessingCallbackIgnoresOlderEventBeforeStatusEvaluation(t *testing.T) {
	lastEventID := "event-completed"
	lastEventSequence := int64(3)
	incomingSequence := int64(2)
	service, commandRepository, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusCompleted,
		&lastEventID,
		&lastEventSequence,
	)

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "running", EventID: "event-running", Sequence: &incomingSequence,
	})

	require.NoError(t, err)
	require.Equal(t, "ignored", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Empty(t, runRepository.updates)
}

func TestProcessingCallbackIgnoresDifferentEventAtAlreadyAppliedSequence(t *testing.T) {
	lastEventID := "event-terminal-a"
	lastEventSequence := int64(3)
	service, commandRepository, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusCompleted,
		&lastEventID,
		&lastEventSequence,
	)

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "failed", EventID: "event-terminal-b", Sequence: &lastEventSequence,
	})

	require.NoError(t, err)
	require.Equal(t, "ignored", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Empty(t, runRepository.updates)
}

func TestProcessingCallbackIgnoresNewerInvalidTerminalTransition(t *testing.T) {
	lastEventID := "event-completed"
	lastEventSequence := int64(3)
	incomingSequence := int64(4)
	service, commandRepository, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusCompleted,
		&lastEventID,
		&lastEventSequence,
	)

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "failed", EventID: "event-invalid", Sequence: &incomingSequence,
	})

	require.NoError(t, err)
	require.Equal(t, "ignored", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Empty(t, runRepository.transitions)
	// The aggregate repository is also untouched because validation happens before persistence.
	require.Empty(t, runRepository.updates)
}

func TestProcessingCallbackPassesFirstOrderedEventToAtomicTransition(t *testing.T) {
	incomingSequence := int64(1)
	service, commandRepository, runRepository := orderedProcessingCallbackFixture(
		entity.InferenceIngestionProcessingJobStatusQueued,
		nil,
		nil,
	)

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", ProcessingRunID: "run-1", StudyInstanceUID: "study-1",
		ModelName: "model-one", Status: "queued", EventID: "event-queued", Sequence: &incomingSequence,
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Empty(t, commandRepository.updates)
	require.Len(t, runRepository.transitions, 1)
	require.Equal(t, "event-queued", *runRepository.transitions[0].EventID)
	require.EqualValues(t, 1, *runRepository.transitions[0].EventSequence)
}

func TestProcessingCallbackRejectsPartialOrderingIdentity(t *testing.T) {
	sequence := int64(1)
	tests := []serviceTypes.HandleStudyServiceProcessingCallback{
		{
			CandidateID: "candidate-1", StudyInstanceUID: "study-1", ModelName: "model-one",
			Status: "queued", EventID: "event-only",
		},
		{
			CandidateID: "candidate-1", StudyInstanceUID: "study-1", ModelName: "model-one",
			Status: "queued", Sequence: &sequence,
		},
	}

	for _, callback := range tests {
		service, commandRepository, runRepository := orderedProcessingCallbackFixture(
			entity.InferenceIngestionProcessingJobStatusPending,
			nil,
			nil,
		)

		_, err := service.HandleStudyServiceProcessingCallback(context.Background(), callback)

		require.EqualError(t, err, apiError.InvalidPayload)
		require.Empty(t, commandRepository.updates)
		require.Empty(t, runRepository.updates)
	}
}

func TestProcessingCallbackLeavesLegacyExecutionAggregationUnchanged(t *testing.T) {
	queryRepository := &processingRunCallbackQueryRepository{
		candidate: entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a", StudyInstanceUID: "study-1"},
		execution: entity.InferenceIngestionProcessingJob{
			ID: "legacy-execution", Status: entity.InferenceIngestionProcessingJobStatusQueued,
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	runRepository := &processingRunAggregationRepository{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	result, err := service.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
		CandidateID: "candidate-1", StudyInstanceUID: "study-1", ModelName: "legacy-model", Status: "running",
	})

	require.NoError(t, err)
	require.Equal(t, "applied", result.Outcome)
	require.Len(t, commandRepository.updates, 1)
	require.Empty(t, runRepository.updates)
}

func TestProcessingCallbackRejectsCorrelatedIdentityMismatchesBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(
			*entity.InferenceIngestionCandidate,
			*entity.InferenceIngestionProcessingRun,
			*entity.InferenceIngestionProcessingJob,
			*serviceTypes.HandleStudyServiceProcessingCallback,
		)
	}{
		{
			name: "tenant",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, _ *entity.InferenceIngestionProcessingJob, callback *serviceTypes.HandleStudyServiceProcessingCallback) {
				callback.TenantID = "tenant-other"
			},
		},
		{
			name: "payload candidate",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, _ *entity.InferenceIngestionProcessingJob, callback *serviceTypes.HandleStudyServiceProcessingCallback) {
				callback.PayloadCandidateID = "candidate-other"
			},
		},
		{
			name: "ingestion job",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, _ *entity.InferenceIngestionProcessingJob, callback *serviceTypes.HandleStudyServiceProcessingCallback) {
				callback.IngestionJobID = "ingestion-other"
			},
		},
		{
			name: "study",
			mutate: func(_ *entity.InferenceIngestionCandidate, run *entity.InferenceIngestionProcessingRun, _ *entity.InferenceIngestionProcessingJob, _ *serviceTypes.HandleStudyServiceProcessingCallback) {
				run.StudyInstanceUID = "study-other"
			},
		},
		{
			name: "run",
			mutate: func(_ *entity.InferenceIngestionCandidate, run *entity.InferenceIngestionProcessingRun, _ *entity.InferenceIngestionProcessingJob, _ *serviceTypes.HandleStudyServiceProcessingCallback) {
				run.ID = "run-other"
			},
		},
		{
			name: "model",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, execution *entity.InferenceIngestionProcessingJob, _ *serviceTypes.HandleStudyServiceProcessingCallback) {
				execution.ModelName = "model-other"
			},
		},
		{
			name: "model version",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, execution *entity.InferenceIngestionProcessingJob, _ *serviceTypes.HandleStudyServiceProcessingCallback) {
				execution.ModelVersion = nonEmptyStringPointer("2.0")
			},
		},
		{
			name: "modality",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, execution *entity.InferenceIngestionProcessingJob, _ *serviceTypes.HandleStudyServiceProcessingCallback) {
				execution.Modality = nonEmptyStringPointer("CT")
			},
		},
		{
			name: "study service job",
			mutate: func(_ *entity.InferenceIngestionCandidate, _ *entity.InferenceIngestionProcessingRun, execution *entity.InferenceIngestionProcessingJob, _ *serviceTypes.HandleStudyServiceProcessingCallback) {
				execution.StudyServiceJobID = nonEmptyStringPointer("python-job-other")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processingRunID := "run-1"
			candidate := entity.InferenceIngestionCandidate{
				ID: "candidate-1", TenantID: "tenant-a", IngestionJobID: "ingestion-1", StudyInstanceUID: "study-1",
			}
			run := entity.InferenceIngestionProcessingRun{
				ID: processingRunID, TenantID: "tenant-a", StudyInstanceUID: "study-1", Version: 1,
			}
			execution := entity.InferenceIngestionProcessingJob{
				ID: "execution-1", ProcessingRunID: &processingRunID, CandidateID: "candidate-1",
				TenantID: "tenant-a", ModelName: "model-one",
				ModelVersion: nonEmptyStringPointer("1.0"), Modality: nonEmptyStringPointer("US"),
				StudyServiceJobID: nonEmptyStringPointer("python-job-1"),
				Status:            entity.InferenceIngestionProcessingJobStatusQueued,
			}
			callback := serviceTypes.HandleStudyServiceProcessingCallback{
				CandidateID: "candidate-1", PayloadCandidateID: "candidate-1",
				TenantID: "tenant-a", IngestionJobID: "ingestion-1",
				ProcessingRunID: processingRunID, StudyInstanceUID: "study-1",
				ModelName: "model-one", ModelVersion: "1.0", Modality: "US",
				StudyServiceJobID: "python-job-1", Status: "running",
			}
			test.mutate(&candidate, &run, &execution, &callback)

			queryRepository := &processingRunCallbackQueryRepository{candidate: candidate}
			commandRepository := &processingRunCallbackCommandRepository{}
			runRepository := &processingRunCallbackRunRepository{
				selectedExecution: execution,
				processingRunAggregationRepository: &processingRunAggregationRepository{
					runs: []entity.InferenceIngestionProcessingRun{run},
				},
			}
			service := &InferenceCommandService{
				InferenceQueryRepositoryInterface:         queryRepository,
				InferenceCommandRepositoryInterface:       commandRepository,
				InferenceProcessingRunRepositoryInterface: runRepository,
			}

			_, err := service.HandleStudyServiceProcessingCallback(context.Background(), callback)

			require.EqualError(t, err, apiError.InvalidPayload)
			require.Empty(t, commandRepository.updates)
			require.Empty(t, runRepository.updates)
		})
	}
}

func TestCorrelatedQueuedDispatchUpdatesPlannedExecution(t *testing.T) {
	processingRunID := "run-1"
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusPending,
		},
		processingRunAggregationRepository: &processingRunAggregationRepository{},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	err := service.recordQueuedProcessingDispatch(
		context.Background(),
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		entity.InferenceIngestionJob{ModelName: "model-one", ModelVersion: "1.0"},
		serviceTypes.DispatchStudyRequest{ProcessingRunID: &processingRunID, Modality: "US"},
		serviceTypes.DispatchStudyResponse{JobID: "study-job-1"},
	)

	require.NoError(t, err)
	require.Len(t, commandRepository.updates, 1)
	require.Equal(t, "execution-1", commandRepository.updates[0].ID)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusQueued, commandRepository.updates[0].Status)
	require.Equal(t, "study-job-1", *commandRepository.updates[0].StudyServiceJobID)
}

func TestCorrelatedFailedDispatchMarksRunForAttention(t *testing.T) {
	processingRunID := "run-1"
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusPending,
		},
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{ID: processingRunID, TenantID: "tenant-a", Version: 3}},
			executions: []entity.InferenceIngestionProcessingJob{{
				ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusFailed,
			}},
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	service.recordFailedProcessingDispatch(
		context.Background(),
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		entity.InferenceIngestionJob{ModelName: "model-one"},
		serviceTypes.DispatchStudyRequest{ProcessingRunID: &processingRunID},
		errors.New("study-service rejected dispatch"),
	)

	require.Len(t, commandRepository.updates, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusFailed, commandRepository.updates[0].Status)
	require.Len(t, runRepository.updates, 1)
	require.True(t, runRepository.updates[0].AttentionRequired)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionDispatchFailed, runRepository.updates[0].AttentionReasons[0].Code)
}
