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

type processingRunAggregationRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	runs         []entity.InferenceIngestionProcessingRun
	executions   []entity.InferenceIngestionProcessingJob
	updates      []repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate
	updateErrors []error
}

func (repository *processingRunAggregationRepository) SelectProcessingRun(_ context.Context, _, _ string) (entity.InferenceIngestionProcessingRun, error) {
	index := len(repository.updates)
	if index >= len(repository.runs) {
		index = len(repository.runs) - 1
	}
	return repository.runs[index], nil
}

func (repository *processingRunAggregationRepository) ListProcessingRunExecutions(_ context.Context, _, _ string) ([]entity.InferenceIngestionProcessingJob, error) {
	return repository.executions, nil
}

func (repository *processingRunAggregationRepository) UpdateProcessingRunAggregate(_ context.Context, data repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error) {
	repository.updates = append(repository.updates, data)
	index := len(repository.updates) - 1
	if index < len(repository.updateErrors) && repository.updateErrors[index] != nil {
		return entity.InferenceIngestionProcessingRun{}, repository.updateErrors[index]
	}
	updated := repository.runs[len(repository.runs)-1]
	updated.Phase = data.Phase
	updated.Outcome = data.Outcome
	updated.AttentionRequired = data.AttentionRequired
	updated.AttentionReasons = data.AttentionReasons
	updated.StartedAt = data.StartedAt
	updated.CompletedAt = data.CompletedAt
	updated.Version = data.ExpectedVersion + 1
	return updated, nil
}

func TestRecalculateStudyProcessingRunPersistsAuthoritativeAggregate(t *testing.T) {
	startedAt := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(time.Minute)
	repository := &processingRunAggregationRepository{
		runs: []entity.InferenceIngestionProcessingRun{{ID: "run-1", TenantID: "tenant-a", Version: 4}},
		executions: []entity.InferenceIngestionProcessingJob{
			{ID: "one", Status: entity.InferenceIngestionProcessingJobStatusCompleted, StartedAt: &startedAt, CompletedAt: &completedAt},
			{ID: "two", Status: entity.InferenceIngestionProcessingJobStatusSkipped},
		},
	}
	service := &InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.RecalculateStudyProcessingRun(context.Background(), serviceTypes.RecalculateStudyProcessingRun{
		TenantID: " tenant-a ", ProcessingRunID: " run-1 ",
	})

	require.NoError(t, err)
	require.Len(t, repository.updates, 1)
	update := repository.updates[0]
	require.Equal(t, "run-1", update.ID)
	require.Equal(t, "tenant-a", update.TenantID)
	require.Equal(t, int64(4), update.ExpectedVersion)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, update.Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomeSuccessWithSkips, *update.Outcome)
	require.Equal(t, startedAt, *update.StartedAt)
	require.Equal(t, completedAt, *update.CompletedAt)
	require.Equal(t, int64(5), result.Run.Version)
	require.Equal(t, 1, result.Counts.Completed)
	require.Equal(t, 1, result.Counts.Skipped)
}

func TestRecalculateStudyProcessingRunRetriesVersionConflict(t *testing.T) {
	repository := &processingRunAggregationRepository{
		runs: []entity.InferenceIngestionProcessingRun{
			{ID: "run-1", TenantID: "tenant-a", Version: 4},
			{ID: "run-1", TenantID: "tenant-a", Version: 5},
		},
		executions: []entity.InferenceIngestionProcessingJob{
			{ID: "one", Status: entity.InferenceIngestionProcessingJobStatusRunning},
		},
		updateErrors: []error{errors.New(apiError.DuplicateRecord)},
	}
	service := &InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.RecalculateStudyProcessingRun(context.Background(), serviceTypes.RecalculateStudyProcessingRun{
		TenantID: "tenant-a", ProcessingRunID: "run-1",
	})

	require.NoError(t, err)
	require.Len(t, repository.updates, 2)
	require.Equal(t, int64(4), repository.updates[0].ExpectedVersion)
	require.Equal(t, int64(5), repository.updates[1].ExpectedVersion)
	require.Equal(t, int64(6), result.Run.Version)
}

func TestRecalculateStudyProcessingRunStopsAfterVersionConflicts(t *testing.T) {
	repository := &processingRunAggregationRepository{
		runs: []entity.InferenceIngestionProcessingRun{{ID: "run-1", TenantID: "tenant-a", Version: 4}},
		executions: []entity.InferenceIngestionProcessingJob{
			{ID: "one", Status: entity.InferenceIngestionProcessingJobStatusRunning},
		},
		updateErrors: []error{
			errors.New(apiError.DuplicateRecord),
			errors.New(apiError.DuplicateRecord),
			errors.New(apiError.DuplicateRecord),
		},
	}
	service := &InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	_, err := service.RecalculateStudyProcessingRun(context.Background(), serviceTypes.RecalculateStudyProcessingRun{
		TenantID: "tenant-a", ProcessingRunID: "run-1",
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.Len(t, repository.updates, processingRunAggregateUpdateAttempts)
}

func TestRecalculateStudyProcessingRunValidatesScope(t *testing.T) {
	service := &InferenceCommandService{}

	_, err := service.RecalculateStudyProcessingRun(context.Background(), serviceTypes.RecalculateStudyProcessingRun{})

	require.EqualError(t, err, apiError.InvalidPayload)
}
