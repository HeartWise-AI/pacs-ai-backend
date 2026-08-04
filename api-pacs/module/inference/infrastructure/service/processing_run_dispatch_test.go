package service

import (
	"context"
	"errors"
	"testing"

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
}

func (repository *guardedDispatchCommandRepository) UpdateCandidateDispatchState(data repositoryTypes.UpdateCandidateDispatchState) error {
	repository.dispatchStateUpdates = append(repository.dispatchStateUpdates, data)
	return nil
}

func (repository *guardedDispatchCommandRepository) UpdateInferenceIngestionProcessingJob(data repositoryTypes.UpdateInferenceIngestionProcessingJob) error {
	repository.executionUpdates = append(repository.executionUpdates, data)
	return nil
}

type guardedProcessingDispatcher struct {
	buildCalls    int
	dispatchCalls int
	response      serviceTypes.DispatchStudyResponse
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
	dispatcher.dispatchCalls++
	return dispatcher.response, nil
}

func (dispatcher *guardedProcessingDispatcher) GetJobsByCandidate(context.Context, string, string) ([]serviceTypes.StudyServiceJob, error) {
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
