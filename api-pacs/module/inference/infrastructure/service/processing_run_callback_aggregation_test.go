package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

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
}

func (repository *processingRunCallbackRunRepository) SelectProcessingRunExecution(context.Context, string, string, string, string) (entity.InferenceIngestionProcessingJob, error) {
	return repository.selectedExecution, nil
}

func (repository *processingRunCallbackCommandRepository) UpdateInferenceIngestionProcessingJob(data repositoryTypes.UpdateInferenceIngestionProcessingJob) error {
	repository.updates = append(repository.updates, data)
	return nil
}

func TestProcessingCallbackRecalculatesLinkedRunAfterAppliedTransition(t *testing.T) {
	processingRunID := "run-1"
	queryRepository := &processingRunCallbackQueryRepository{
		candidate: entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a", StudyInstanceUID: "study-1"},
		execution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusQueued,
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: queryRepository.execution,
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{ID: processingRunID, TenantID: "tenant-a", Version: 2}},
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
	require.Len(t, commandRepository.updates, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusRunning, commandRepository.updates[0].Status)
	require.Len(t, runRepository.updates, 1)
	require.Equal(t, int64(2), runRepository.updates[0].ExpectedVersion)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseProcessing, runRepository.updates[0].Phase)
}

func TestProcessingCallbackReplayHealsLinkedRunAggregate(t *testing.T) {
	processingRunID := "run-1"
	queryRepository := &processingRunCallbackQueryRepository{
		candidate: entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a", StudyInstanceUID: "study-1"},
		execution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusRunning,
		},
	}
	commandRepository := &processingRunCallbackCommandRepository{}
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: queryRepository.execution,
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{ID: processingRunID, TenantID: "tenant-a", Version: 7}},
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
	require.Len(t, runRepository.updates, 1)
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
