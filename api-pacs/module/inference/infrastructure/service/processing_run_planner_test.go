package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type processingRunPlannerQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	candidates []entity.InferenceIngestionCandidate
	jobs       map[string]entity.InferenceIngestionJob
	err        error
}

func (repository *processingRunPlannerQueryRepository) ListInferenceIngestionCandidates(data repositoryTypes.ListInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error) {
	if repository.err != nil {
		return nil, repository.err
	}
	return repository.candidates, nil
}

func (repository *processingRunPlannerQueryRepository) SelectInferenceIngestionJobByID(id string) (entity.InferenceIngestionJob, error) {
	job, ok := repository.jobs[id]
	if !ok {
		return entity.InferenceIngestionJob{}, errors.New(apiError.MissingRecord)
	}
	return job, nil
}

type processingRunPlannerRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	activeRun        *entity.InferenceIngestionProcessingRun
	activeErr        error
	activeExecutions []entity.InferenceIngestionProcessingJob
	create           func(repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error)
}

type concurrentProcessingRunPlannerRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	mu         sync.Mutex
	activeRun  *entity.InferenceIngestionProcessingRun
	executions []entity.InferenceIngestionProcessingJob
	created    int
}

func (repository *concurrentProcessingRunPlannerRepository) SelectActiveProcessingRun(_ context.Context, _, _ string) (entity.InferenceIngestionProcessingRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.activeRun == nil {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return *repository.activeRun, nil
}

func (repository *concurrentProcessingRunPlannerRepository) ListProcessingRunExecutions(_ context.Context, _, _ string) ([]entity.InferenceIngestionProcessingJob, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]entity.InferenceIngestionProcessingJob(nil), repository.executions...), nil
}

func (repository *concurrentProcessingRunPlannerRepository) CreateProcessingRunPlan(_ context.Context, data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.activeRun != nil {
		return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{
			Run: *repository.activeRun, Executions: append([]entity.InferenceIngestionProcessingJob(nil), repository.executions...), Created: false,
		}, nil
	}

	run := entity.InferenceIngestionProcessingRun{
		ID: data.Run.ID, TenantID: data.Run.TenantID, StudyInstanceUID: data.Run.StudyInstanceUID,
		RunNumber: 1, RunTrigger: data.Run.RunTrigger, Phase: data.Run.Phase,
	}
	executions := make([]entity.InferenceIngestionProcessingJob, 0, len(data.Executions))
	for _, expected := range data.Executions {
		executions = append(executions, entity.InferenceIngestionProcessingJob{
			ID: expected.ID, ProcessingRunID: &run.ID, CandidateID: expected.CandidateID,
			TenantID: run.TenantID, ModelName: expected.ModelName, Status: entity.InferenceIngestionProcessingJobStatusPending,
		})
	}
	repository.activeRun = &run
	repository.executions = executions
	repository.created++
	return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{Run: run, Executions: executions, Created: true}, nil
}

func (repository *processingRunPlannerRepository) SelectActiveProcessingRun(_ context.Context, _, _ string) (entity.InferenceIngestionProcessingRun, error) {
	if repository.activeErr != nil {
		return entity.InferenceIngestionProcessingRun{}, repository.activeErr
	}
	if repository.activeRun == nil {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return *repository.activeRun, nil
}

func (repository *processingRunPlannerRepository) ListProcessingRunExecutions(_ context.Context, _, _ string) ([]entity.InferenceIngestionProcessingJob, error) {
	return repository.activeExecutions, nil
}

func (repository *processingRunPlannerRepository) CreateProcessingRunPlan(_ context.Context, data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
	return repository.create(data)
}

func TestCreateAutomaticStudyProcessingRunFreezesAllKnownModels(t *testing.T) {
	queryRepository := &processingRunPlannerQueryRepository{
		candidates: []entity.InferenceIngestionCandidate{
			{ID: "candidate-b", TenantID: "tenant-a", IngestionJobID: "job-b", StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusStable},
			{ID: "candidate-gone", TenantID: "tenant-a", IngestionJobID: "job-gone", StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusDisappeared},
			{ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: "job-a", StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved},
		},
		jobs: map[string]entity.InferenceIngestionJob{
			"job-a": {ID: "job-a", TenantID: "tenant-a", ModelName: "AlphaModel", ModelVersion: "v1", DICOMModality: "US"},
			"job-b": {ID: "job-b", TenantID: "tenant-a", ModelName: "BetaModel", ModelVersion: "v2", DICOMModality: "US"},
		},
	}
	runRepository := &processingRunPlannerRepository{
		create: func(data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
			require.Equal(t, "tenant-a", data.Run.TenantID)
			require.Equal(t, "1.2.3", data.Run.StudyInstanceUID)
			require.Equal(t, entity.InferenceIngestionProcessingRunTriggerAuto, data.Run.RunTrigger)
			require.Equal(t, entity.InferenceIngestionProcessingRunPhaseQueued, data.Run.Phase)
			require.NotEmpty(t, data.Run.ID)
			require.Len(t, data.Executions, 2)
			require.Equal(t, "AlphaModel", data.Executions[0].ModelName)
			require.Equal(t, "candidate-a", data.Executions[0].CandidateID)
			require.Equal(t, "BetaModel", data.Executions[1].ModelName)
			require.Equal(t, "candidate-b", data.Executions[1].CandidateID)
			return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{
				Run: entity.InferenceIngestionProcessingRun{ID: "run-1"},
				Executions: []entity.InferenceIngestionProcessingJob{
					{ID: "execution-a"}, {ID: "execution-b"},
				},
				Created: true,
			}, nil
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	result, err := service.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: " tenant-a ", StudyInstanceUID: " 1.2.3 ",
	})

	require.NoError(t, err)
	require.True(t, result.Created)
	require.Equal(t, "run-1", result.Run.ID)
	require.Len(t, result.Executions, 2)
}

func TestCreateManualStudyProcessingRunPropagatesActiveRunConflict(t *testing.T) {
	activeRun := entity.InferenceIngestionProcessingRun{ID: "run-1"}
	runRepository := &processingRunPlannerRepository{
		activeRun: &activeRun,
		create: func(data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
			t.Fatal("repository create must not be called while a run is active")
			return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{}, nil
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         &processingRunPlannerQueryRepository{},
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	_, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
}

func TestCreateAutomaticStudyProcessingRunReusesFrozenActivePlan(t *testing.T) {
	activeRun := entity.InferenceIngestionProcessingRun{ID: "run-1", RunNumber: 1}
	runRepository := &processingRunPlannerRepository{
		activeRun:        &activeRun,
		activeExecutions: []entity.InferenceIngestionProcessingJob{{ID: "execution-a"}},
		create: func(repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
			t.Fatal("repository create must not be called while an automatic run is active")
			return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{}, nil
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         &processingRunPlannerQueryRepository{err: errors.New("candidate state must not be reloaded")},
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	result, err := service.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.False(t, result.Created)
	require.Equal(t, "run-1", result.Run.ID)
	require.Len(t, result.Executions, 1)
}

func TestCreateStudyProcessingRunRejectsEmptyExpectedPlan(t *testing.T) {
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface: &processingRunPlannerQueryRepository{
			candidates: []entity.InferenceIngestionCandidate{{
				ID: "candidate-gone", TenantID: "tenant-a", IngestionJobID: "job-a",
				StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusDisappeared,
			}},
			jobs: map[string]entity.InferenceIngestionJob{},
		},
		InferenceProcessingRunRepositoryInterface: &processingRunPlannerRepository{
			create: func(repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
				t.Fatal("repository must not be called for an empty plan")
				return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{}, nil
			},
		},
	}

	_, err := service.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.MissingRecord)
}

func TestConcurrentAutomaticSchedulingConvergesOnOneFrozenPlan(t *testing.T) {
	queryRepository := &processingRunPlannerQueryRepository{
		candidates: []entity.InferenceIngestionCandidate{{
			ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: "job-a",
			StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved,
		}},
		jobs: map[string]entity.InferenceIngestionJob{
			"job-a": {ID: "job-a", TenantID: "tenant-a", ModelName: "AlphaModel"},
		},
	}
	runRepository := &concurrentProcessingRunPlannerRepository{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	const callers = 8
	results := make(chan serviceTypes.CreateStudyProcessingRunResult, callers)
	errorsChannel := make(chan error, callers)
	var waitGroup sync.WaitGroup
	for range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := service.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
				TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			})
			results <- result
			errorsChannel <- err
		}()
	}
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	for err := range errorsChannel {
		require.NoError(t, err)
	}
	created := 0
	runID := ""
	for result := range results {
		if result.Created {
			created++
		}
		if runID == "" {
			runID = result.Run.ID
		}
		require.Equal(t, runID, result.Run.ID)
		require.Len(t, result.Executions, 1)
	}
	require.Equal(t, 1, created)
	require.Equal(t, 1, runRepository.created)
}
