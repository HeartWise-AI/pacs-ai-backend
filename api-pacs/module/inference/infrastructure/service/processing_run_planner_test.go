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
	create func(repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error)
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
	queryRepository := &processingRunPlannerQueryRepository{
		candidates: []entity.InferenceIngestionCandidate{{
			ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: "job-a",
			StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved,
		}},
		jobs: map[string]entity.InferenceIngestionJob{
			"job-a": {ID: "job-a", TenantID: "tenant-a", ModelName: "AlphaModel"},
		},
	}
	runRepository := &processingRunPlannerRepository{
		create: func(data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
			require.Equal(t, entity.InferenceIngestionProcessingRunTriggerManualReprocess, data.Run.RunTrigger)
			return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{}, errors.New(apiError.DuplicateRecord)
		},
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
	}

	_, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
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
