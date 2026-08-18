package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/application"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type executionResultRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	run                  entity.InferenceIngestionProcessingRun
	execution            entity.InferenceIngestionProcessingJob
	runErr               error
	executionErr         error
	runTenantID          string
	runID                string
	executionTenantID    string
	executionRunID       string
	requestedExecutionID string
}

func (repository *executionResultRepository) SelectProcessingRun(_ context.Context, tenantID, runID string) (entity.InferenceIngestionProcessingRun, error) {
	repository.runTenantID = tenantID
	repository.runID = runID
	return repository.run, repository.runErr
}

func (repository *executionResultRepository) SelectProcessingRunExecutionByID(_ context.Context, tenantID, runID, executionID string) (entity.InferenceIngestionProcessingJob, error) {
	repository.executionTenantID = tenantID
	repository.executionRunID = runID
	repository.requestedExecutionID = executionID
	return repository.execution, repository.executionErr
}

type executionResultDispatcher struct {
	application.ProcessingResultProviderInterface
	job      serviceTypes.StudyServiceJobResult
	found    bool
	err      error
	tenantID string
	jobID    string
	calls    int
}

func (dispatcher *executionResultDispatcher) GetJobResultByID(_ context.Context, tenantID, jobID string) (serviceTypes.StudyServiceJobResult, bool, error) {
	dispatcher.calls++
	dispatcher.tenantID = tenantID
	dispatcher.jobID = jobID
	return dispatcher.job, dispatcher.found, dispatcher.err
}

func validExecutionResultFixture() (*executionResultRepository, *executionResultDispatcher, serviceTypes.GetProcessingRunExecutionResult) {
	completedAt := time.Date(2026, 8, 11, 14, 30, 0, 0, time.UTC)
	runID := "run-1"
	jobID := "python-job-1"
	modelVersion := "1.0.0"
	tenantID := "tenant-a"
	candidateID := "candidate-1"
	executionID := "execution-1"
	repository := &executionResultRepository{
		run: entity.InferenceIngestionProcessingRun{
			ID: runID, TenantID: tenantID, StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess,
		},
		execution: entity.InferenceIngestionProcessingJob{
			ID: executionID, ProcessingRunID: &runID, CandidateID: candidateID, TenantID: tenantID,
			ModelName: "CardioSyntax", ModelVersion: &modelVersion,
			Status:            entity.InferenceIngestionProcessingJobStatusCompleted,
			StudyServiceJobID: &jobID, CompletedAt: &completedAt,
		},
	}
	dispatcher := &executionResultDispatcher{
		found: true,
		job: serviceTypes.StudyServiceJobResult{
			StudyServiceJob: serviceTypes.StudyServiceJob{
				JobID: jobID, StudyInstanceUID: "1.2.3", TenantID: &tenantID,
				CandidateID: &candidateID, ProcessingRunID: &runID, ProcessingExecutionID: &executionID,
				ModelName: "CardioSyntax", ModelVersion: &modelVersion, Status: "completed", CompletedAt: &completedAt,
			},
			ResultJSON: json.RawMessage(`{"syntax_score":24.5}`),
		},
	}
	return repository, dispatcher, serviceTypes.GetProcessingRunExecutionResult{
		TenantID: tenantID, RunID: runID, ExecutionID: executionID,
	}
}

func TestGetProcessingRunExecutionResultAcceptsAutomaticJobWithoutExecutionIdentity(t *testing.T) {
	repository, dispatcher, input := validExecutionResultFixture()
	repository.run.RunTrigger = entity.InferenceIngestionProcessingRunTriggerAuto
	dispatcher.job.ProcessingExecutionID = nil
	service := InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: repository,
		ProcessingResultProviderInterface:         dispatcher,
	}

	result, err := service.GetProcessingRunExecutionResult(context.Background(), input)

	require.NoError(t, err)
	require.Equal(t, input.ExecutionID, result.ExecutionID)
	require.JSONEq(t, `{"syntax_score":24.5}`, string(result.Result))
}

func TestGetProcessingRunExecutionResultRejectsForeignAutomaticExecutionIdentity(t *testing.T) {
	repository, dispatcher, input := validExecutionResultFixture()
	repository.run.RunTrigger = entity.InferenceIngestionProcessingRunTriggerAuto
	otherExecutionID := "execution-2"
	dispatcher.job.ProcessingExecutionID = &otherExecutionID
	service := InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: repository,
		ProcessingResultProviderInterface:         dispatcher,
	}

	_, err := service.GetProcessingRunExecutionResult(context.Background(), input)

	require.EqualError(t, err, apiError.InferenceExecutionResultInvalid)
}

func TestGetProcessingRunExecutionResultValidatesAllCorrelations(t *testing.T) {
	repository, dispatcher, input := validExecutionResultFixture()
	service := InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: repository,
		ProcessingResultProviderInterface:         dispatcher,
	}

	result, err := service.GetProcessingRunExecutionResult(context.Background(), input)

	require.NoError(t, err)
	require.Equal(t, "tenant-a", repository.runTenantID)
	require.Equal(t, "tenant-a", repository.executionTenantID)
	require.Equal(t, "run-1", repository.executionRunID)
	require.Equal(t, "execution-1", repository.requestedExecutionID)
	require.Equal(t, "tenant-a", dispatcher.tenantID)
	require.Equal(t, "python-job-1", dispatcher.jobID)
	require.Equal(t, "1.2.3", result.StudyInstanceUID)
	require.JSONEq(t, `{"syntax_score":24.5}`, string(result.Result))
}

func TestGetProcessingRunExecutionResultRejectsNonCompletedBeforeUpstreamLookup(t *testing.T) {
	repository, dispatcher, input := validExecutionResultFixture()
	repository.execution.Status = entity.InferenceIngestionProcessingJobStatusRunning
	service := InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: repository,
		ProcessingResultProviderInterface:         dispatcher,
	}

	_, err := service.GetProcessingRunExecutionResult(context.Background(), input)

	require.EqualError(t, err, apiError.InferenceExecutionResultNotAvailable)
	require.Zero(t, dispatcher.calls)
}

func TestGetProcessingRunExecutionResultMapsLookupFailuresSafely(t *testing.T) {
	t.Run("missing local execution remains not found", func(t *testing.T) {
		repository, dispatcher, input := validExecutionResultFixture()
		repository.executionErr = errors.New(apiError.MissingRecord)
		service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository, ProcessingResultProviderInterface: dispatcher}

		_, err := service.GetProcessingRunExecutionResult(context.Background(), input)

		require.EqualError(t, err, apiError.MissingRecord)
		require.Zero(t, dispatcher.calls)
	})

	t.Run("upstream transport is retryable unavailable", func(t *testing.T) {
		repository, dispatcher, input := validExecutionResultFixture()
		dispatcher.err = errors.New("sensitive upstream detail")
		service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository, ProcessingResultProviderInterface: dispatcher}

		_, err := service.GetProcessingRunExecutionResult(context.Background(), input)

		require.EqualError(t, err, apiError.InferenceResultServiceUnavailable)
		require.NotContains(t, err.Error(), "sensitive")
	})

	t.Run("missing authoritative job is invalid completed result", func(t *testing.T) {
		repository, dispatcher, input := validExecutionResultFixture()
		dispatcher.found = false
		service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository, ProcessingResultProviderInterface: dispatcher}

		_, err := service.GetProcessingRunExecutionResult(context.Background(), input)

		require.EqualError(t, err, apiError.InferenceExecutionResultInvalid)
	})
}

func TestGetProcessingRunExecutionResultRejectsMismatchedOrInvalidAuthoritativeData(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*executionResultRepository, *executionResultDispatcher)
	}{
		{name: "tenant mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			other := "tenant-b"
			dispatcher.job.TenantID = &other
		}},
		{name: "run mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			other := "run-2"
			dispatcher.job.ProcessingRunID = &other
		}},
		{name: "execution mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			other := "execution-2"
			dispatcher.job.ProcessingExecutionID = &other
		}},
		{name: "manual execution missing", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.ProcessingExecutionID = nil
		}},
		{name: "candidate mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			other := "candidate-2"
			dispatcher.job.CandidateID = &other
		}},
		{name: "study mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.StudyInstanceUID = "9.9.9"
		}},
		{name: "model mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.ModelName = "OtherModel"
		}},
		{name: "version mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			other := "2.0.0"
			dispatcher.job.ModelVersion = &other
		}},
		{name: "job mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.JobID = "other-job"
		}},
		{name: "status mismatch", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.Status = "running"
		}},
		{name: "malformed result", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.ResultJSON = json.RawMessage(`{"broken"`)
		}},
		{name: "null result", mutate: func(_ *executionResultRepository, dispatcher *executionResultDispatcher) {
			dispatcher.job.ResultJSON = json.RawMessage(`null`)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, dispatcher, input := validExecutionResultFixture()
			test.mutate(repository, dispatcher)
			service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository, ProcessingResultProviderInterface: dispatcher}

			_, err := service.GetProcessingRunExecutionResult(context.Background(), input)

			require.EqualError(t, err, apiError.InferenceExecutionResultInvalid)
		})
	}
}
