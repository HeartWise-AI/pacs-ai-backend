package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type committedExecutionRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	execution entity.InferenceIngestionProcessingJob
	err       error
	calls     int
}

func (repository *committedExecutionRepository) SelectProcessingRunExecution(context.Context, string, string, string, string) (entity.InferenceIngestionProcessingJob, error) {
	repository.calls++
	return repository.execution, repository.err
}

type guardedDispatchCommandRepository struct {
	domainRepository.InferenceCommandRepositoryInterface
	dispatchStateUpdates []repositoryTypes.UpdateCandidateDispatchState
	executionUpdates     []repositoryTypes.UpdateInferenceIngestionProcessingJob
	executionUpdateErr   error
	executionInserts     []repositoryTypes.AddInferenceIngestionProcessingJob
	executionInsertErr   error
}

func (repository *guardedDispatchCommandRepository) UpdateCandidateDispatchState(data repositoryTypes.UpdateCandidateDispatchState) error {
	repository.dispatchStateUpdates = append(repository.dispatchStateUpdates, data)
	return nil
}

func (repository *guardedDispatchCommandRepository) UpdateInferenceIngestionProcessingJob(data repositoryTypes.UpdateInferenceIngestionProcessingJob) error {
	repository.executionUpdates = append(repository.executionUpdates, data)
	return repository.executionUpdateErr
}

func (repository *guardedDispatchCommandRepository) InsertInferenceIngestionProcessingJob(data repositoryTypes.AddInferenceIngestionProcessingJob) error {
	repository.executionInserts = append(repository.executionInserts, data)
	return repository.executionInsertErr
}

type guardedProcessingDispatcher struct {
	buildCalls        int
	dispatchCalls     int
	response          serviceTypes.DispatchStudyResponse
	dispatchResponses []serviceTypes.DispatchStudyResponse
	dispatchErrors    []error
}

func (dispatcher *guardedProcessingDispatcher) BuildDispatchStudyRequest(_ context.Context, data serviceTypes.BuildStudyServiceDispatchRequestInput) (serviceTypes.DispatchStudyRequest, error) {
	dispatcher.buildCalls++
	return serviceTypes.DispatchStudyRequest{
		ProcessingRunID: data.ProcessingRunID,
		Modality:        "US",
		ModelName:       data.IngestionJob.ModelName,
	}, nil
}

func (dispatcher *guardedProcessingDispatcher) DispatchStudy(context.Context, serviceTypes.DispatchStudyRequest) (serviceTypes.DispatchStudyResponse, error) {
	index := dispatcher.dispatchCalls
	dispatcher.dispatchCalls++
	if index < len(dispatcher.dispatchErrors) && dispatcher.dispatchErrors[index] != nil {
		return serviceTypes.DispatchStudyResponse{}, dispatcher.dispatchErrors[index]
	}
	if index < len(dispatcher.dispatchResponses) {
		return dispatcher.dispatchResponses[index], nil
	}
	return dispatcher.response, nil
}

func (dispatcher *guardedProcessingDispatcher) GetJobByID(context.Context, string, string) (serviceTypes.StudyServiceJob, bool, error) {
	return serviceTypes.StudyServiceJob{}, false, nil
}

func (dispatcher *guardedProcessingDispatcher) GetJobsByProcessingRun(context.Context, string, string) ([]serviceTypes.StudyServiceJob, error) {
	return nil, nil
}

func (dispatcher *guardedProcessingDispatcher) GetJobsByCandidate(context.Context, string, string) ([]serviceTypes.StudyServiceJob, error) {
	return nil, nil
}

func (dispatcher *guardedProcessingDispatcher) GetCallbackDeadLetters(context.Context, string) ([]serviceTypes.StudyServiceCallbackDeadLetter, error) {
	return nil, nil
}

func TestDispatchRejectsExecutionOutsideCommittedRunBeforeCallingStudyService(t *testing.T) {
	runRepository := &committedExecutionRepository{err: errors.New(apiError.MissingRecord)}
	commandRepository := &guardedDispatchCommandRepository{}
	dispatcher := &guardedProcessingDispatcher{}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "model-one"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		"run-1",
		"request-1",
	)

	require.ErrorContains(t, err, "outside committed processing run")
	require.Equal(t, 1, runRepository.calls)
	require.Zero(t, dispatcher.buildCalls)
	require.Zero(t, dispatcher.dispatchCalls)
	require.Len(t, commandRepository.dispatchStateUpdates, 1)
	require.NotNil(t, commandRepository.dispatchStateUpdates[0].LastDispatchError)
}

func TestDispatchSkipsExecutionThatAlreadyAdvancedBeyondPending(t *testing.T) {
	runRepository := &committedExecutionRepository{execution: entity.InferenceIngestionProcessingJob{
		ID: "execution-1", Status: entity.InferenceIngestionProcessingJobStatusQueued,
	}}
	dispatcher := &guardedProcessingDispatcher{}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       &guardedDispatchCommandRepository{},
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "model-one"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		"run-1",
		"request-1",
	)

	require.NoError(t, err)
	require.Equal(t, 1, runRepository.calls)
	require.Zero(t, dispatcher.buildCalls)
	require.Zero(t, dispatcher.dispatchCalls)
}

func TestDispatchCallsStudyServiceForCommittedPendingExecution(t *testing.T) {
	runRepository := &committedExecutionRepository{execution: entity.InferenceIngestionProcessingJob{
		ID: "execution-1", Status: entity.InferenceIngestionProcessingJobStatusPending,
	}}
	commandRepository := &guardedDispatchCommandRepository{}
	dispatcher := &guardedProcessingDispatcher{response: serviceTypes.DispatchStudyResponse{JobID: "study-job-1"}}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "model-one", ModelVersion: "1.0"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		"run-1",
		"request-1",
	)

	require.NoError(t, err)
	require.Equal(t, 1, dispatcher.buildCalls)
	require.Equal(t, 1, dispatcher.dispatchCalls)
	require.Len(t, commandRepository.executionUpdates, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusQueued, commandRepository.executionUpdates[0].Status)
	require.Equal(t, "study-job-1", *commandRepository.executionUpdates[0].StudyServiceJobID)
}

func TestAcceptedDispatchReturnsErrorWhenJobCorrelationCannotBePersisted(t *testing.T) {
	processingRunID := "run-1"
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusPending,
		},
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{ID: processingRunID, TenantID: "tenant-a", Version: 1}},
			executions: []entity.InferenceIngestionProcessingJob{{
				ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusPending,
			}},
		},
	}
	commandRepository := &guardedDispatchCommandRepository{executionUpdateErr: errors.New("database unavailable")}
	dispatcher := &guardedProcessingDispatcher{response: serviceTypes.DispatchStudyResponse{JobID: "study-job-1"}}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "model-one"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		processingRunID,
		"request-1",
	)

	require.ErrorContains(t, err, "accepted job but Go could not persist dispatch state")
	require.Equal(t, 1, dispatcher.dispatchCalls)
	require.Len(t, commandRepository.dispatchStateUpdates, 1)
	require.NotNil(t, commandRepository.dispatchStateUpdates[0].LastDispatchError)
	require.Len(t, runRepository.updates, 1)
	require.True(t, runRepository.updates[0].AttentionRequired)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionDispatchFailed, runRepository.updates[0].AttentionReasons[0].Code)
}

func TestDispatchRetriesTransientResponseAndPersistsAcceptedJob(t *testing.T) {
	originalRetrySchedule := studyServiceDispatchRetrySchedule
	studyServiceDispatchRetrySchedule = []time.Duration{0, 0}
	t.Cleanup(func() { studyServiceDispatchRetrySchedule = originalRetrySchedule })

	runRepository := &committedExecutionRepository{execution: entity.InferenceIngestionProcessingJob{
		ID: "execution-1", Status: entity.InferenceIngestionProcessingJobStatusPending,
	}}
	commandRepository := &guardedDispatchCommandRepository{}
	dispatcher := &guardedProcessingDispatcher{
		dispatchResponses: []serviceTypes.DispatchStudyResponse{{}, {JobID: "study-job-1"}},
		dispatchErrors: []error{
			&DispatchStudyHTTPError{StatusCode: http.StatusServiceUnavailable},
			nil,
		},
	}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "model-one"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		"run-1",
		"request-1",
	)

	require.NoError(t, err)
	require.Equal(t, 2, dispatcher.dispatchCalls)
	require.Len(t, commandRepository.executionUpdates, 1)
	require.Equal(t, "study-job-1", *commandRepository.executionUpdates[0].StudyServiceJobID)
}

func TestDispatchDoesNotRetryPermanentResponse(t *testing.T) {
	processingRunID := "run-1"
	runRepository := &processingRunCallbackRunRepository{
		selectedExecution: entity.InferenceIngestionProcessingJob{
			ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusPending,
		},
		processingRunAggregationRepository: &processingRunAggregationRepository{
			runs: []entity.InferenceIngestionProcessingRun{{ID: processingRunID, TenantID: "tenant-a", Version: 1}},
			executions: []entity.InferenceIngestionProcessingJob{{
				ID: "execution-1", ProcessingRunID: &processingRunID, Status: entity.InferenceIngestionProcessingJobStatusFailed,
			}},
		},
	}
	commandRepository := &guardedDispatchCommandRepository{}
	dispatcher := &guardedProcessingDispatcher{dispatchErrors: []error{
		&DispatchStudyHTTPError{StatusCode: http.StatusBadRequest, Body: "invalid model"},
	}}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "model-one"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		processingRunID,
		"request-1",
	)

	require.Error(t, err)
	require.Equal(t, 1, dispatcher.dispatchCalls)
	require.Len(t, commandRepository.executionUpdates, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusFailed, commandRepository.executionUpdates[0].Status)
	require.Len(t, runRepository.updates, 1)
	require.True(t, runRepository.updates[0].AttentionRequired)
}

func TestLegacyDispatchWithoutProcessingRunIDRemainsSupported(t *testing.T) {
	runRepository := &committedExecutionRepository{}
	commandRepository := &guardedDispatchCommandRepository{}
	dispatcher := &guardedProcessingDispatcher{response: serviceTypes.DispatchStudyResponse{JobID: "legacy-study-job-1"}}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       commandRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		ProcessingDispatcherInterface:             dispatcher,
	}

	err := service.dispatchRetrievedCandidateToStudyService(
		context.Background(),
		entity.InferenceIngestionJob{ID: "ingestion-1", ModelName: "legacy-model", ModelVersion: "1.0"},
		entity.InferenceIngestionCandidate{ID: "candidate-1", TenantID: "tenant-a"},
		"",
		"request-1",
	)

	require.NoError(t, err)
	require.Zero(t, runRepository.calls)
	require.Equal(t, 1, dispatcher.dispatchCalls)
	require.Len(t, commandRepository.executionInserts, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusQueued, commandRepository.executionInserts[0].Status)
	require.Equal(t, "legacy-study-job-1", *commandRepository.executionInserts[0].StudyServiceJobID)
}
