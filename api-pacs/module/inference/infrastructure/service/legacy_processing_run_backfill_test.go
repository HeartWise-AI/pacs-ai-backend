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

type legacyBackfillRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	rows         []repositoryTypes.LegacyProcessingRunBackfillRow
	rowResponses [][]repositoryTypes.LegacyProcessingRunBackfillRow
	err          error
	listCalls    int
	imports      []repositoryTypes.ImportLegacyProcessingRun
	importFn     func(repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error)
}

func (repository *legacyBackfillRepository) ListLegacyProcessingRunBackfillRows(context.Context) ([]repositoryTypes.LegacyProcessingRunBackfillRow, error) {
	repository.listCalls++
	if len(repository.rowResponses) > 0 {
		index := repository.listCalls - 1
		if index >= len(repository.rowResponses) {
			index = len(repository.rowResponses) - 1
		}
		return repository.rowResponses[index], repository.err
	}
	return repository.rows, repository.err
}

func (repository *legacyBackfillRepository) ImportLegacyProcessingRun(_ context.Context, data repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error) {
	repository.imports = append(repository.imports, data)
	if repository.importFn != nil {
		return repository.importFn(data)
	}
	return repositoryTypes.ImportLegacyProcessingRunResult{LinkedExecutions: 1}, nil
}

func legacyBackfillRow(executionID, candidateID, tenantID, studyUID, modelName string, status entity.InferenceIngestionProcessingJobStatus) repositoryTypes.LegacyProcessingRunBackfillRow {
	return repositoryTypes.LegacyProcessingRunBackfillRow{
		ExecutionID: executionID, CandidateID: candidateID,
		ExecutionTenantID: tenantID, CandidateTenantID: tenantID,
		StudyInstanceUID: studyUID, ModelName: modelName, Status: status,
	}
}

func TestPlanLegacyProcessingRunBackfillReportsEligibleAndBoundedSkippedGroups(t *testing.T) {
	rows := []repositoryTypes.LegacyProcessingRunBackfillRow{
		legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-eligible", "model-one", entity.InferenceIngestionProcessingJobStatusCompleted),
		legacyBackfillRow("execution-2", "candidate-2", "tenant-a", "study-eligible", "model-two", entity.InferenceIngestionProcessingJobStatusCompleted),
		func() repositoryTypes.LegacyProcessingRunBackfillRow {
			row := legacyBackfillRow("execution-3", "candidate-3", "tenant-a", "study-existing", "model-three", entity.InferenceIngestionProcessingJobStatusQueued)
			row.ExistingRun = true
			return row
		}(),
		legacyBackfillRow("execution-4", "candidate-4", "tenant-a", "study-duplicate", "Model-Four", entity.InferenceIngestionProcessingJobStatusFailed),
		legacyBackfillRow("execution-5", "candidate-5", "tenant-a", "study-duplicate", " model-four ", entity.InferenceIngestionProcessingJobStatusFailed),
		func() repositoryTypes.LegacyProcessingRunBackfillRow {
			row := legacyBackfillRow("execution-6", "candidate-6", "tenant-a", "study-mismatch", "model-six", entity.InferenceIngestionProcessingJobStatusQueued)
			row.CandidateTenantID = "tenant-b"
			return row
		}(),
		legacyBackfillRow("execution-7", "candidate-7", "tenant-a", "study-invalid-status", "model-seven", entity.InferenceIngestionProcessingJobStatus("unknown")),
		legacyBackfillRow("execution-8", "candidate-8", "tenant-a", "", "model-eight", entity.InferenceIngestionProcessingJobStatusQueued),
	}

	report := PlanLegacyProcessingRunBackfill(rows)

	require.Equal(t, 8, report.OrphanExecutions)
	require.Equal(t, 8, report.Candidates)
	require.Equal(t, 6, report.StudyGroups)
	require.Equal(t, 1, report.EligibleStudies)
	require.Equal(t, 2, report.EligibleExecutions)
	require.Equal(t, 5, report.SkippedStudies)
	require.Equal(t, 6, report.SkippedExecutions)
	require.Equal(t, map[string]int{"completed": 2}, report.EligibleStatuses)
	require.Equal(t, map[string]int{
		serviceTypes.LegacyBackfillSkipExistingRun:     1,
		serviceTypes.LegacyBackfillSkipDuplicateModel:  1,
		serviceTypes.LegacyBackfillSkipTenantMismatch:  1,
		serviceTypes.LegacyBackfillSkipInvalidStatus:   1,
		serviceTypes.LegacyBackfillSkipInvalidIdentity: 1,
	}, report.SkipReasons)
}

func TestDryRunLegacyProcessingRunBackfillUsesRepositoryAndReturnsEmptyMaps(t *testing.T) {
	repository := &legacyBackfillRepository{rows: []repositoryTypes.LegacyProcessingRunBackfillRow{}}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	report, err := service.DryRunLegacyProcessingRunBackfill(context.Background())

	require.NoError(t, err)
	require.NotNil(t, report.EligibleStatuses)
	require.NotNil(t, report.SkipReasons)
	require.Empty(t, report.EligibleStatuses)
	require.Empty(t, report.SkipReasons)
}

func TestDryRunLegacyProcessingRunBackfillPropagatesRepositoryFailure(t *testing.T) {
	repository := &legacyBackfillRepository{err: errors.New(apiError.DatabaseError)}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	_, err := service.DryRunLegacyProcessingRunBackfill(context.Background())

	require.EqualError(t, err, apiError.DatabaseError)
}

func TestApplyLegacyProcessingRunBackfillRequiresExactFreshPlanBeforeWriting(t *testing.T) {
	repository := &legacyBackfillRepository{rows: []repositoryTypes.LegacyProcessingRunBackfillRow{
		legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-a", "model-one", entity.InferenceIngestionProcessingJobStatusCompleted),
	}}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillConfirmation, ExpectedStudies: 2, ExpectedExecutions: 1,
	})

	require.EqualError(t, err, apiError.InvalidPayload)
	require.Equal(t, 1, result.Plan.EligibleStudies)
	require.Empty(t, repository.imports)
}

func TestApplyLegacyProcessingRunBackfillRejectsAmbiguityBeforeWriting(t *testing.T) {
	first := legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-a", "model-one", entity.InferenceIngestionProcessingJobStatusCompleted)
	second := legacyBackfillRow("execution-2", "candidate-2", "tenant-a", "study-a", "MODEL-ONE", entity.InferenceIngestionProcessingJobStatusCompleted)
	eligible := legacyBackfillRow("execution-3", "candidate-3", "tenant-a", "study-b", "model-two", entity.InferenceIngestionProcessingJobStatusCompleted)
	repository := &legacyBackfillRepository{rows: []repositoryTypes.LegacyProcessingRunBackfillRow{first, second, eligible}}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	_, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillConfirmation, ExpectedStudies: 1, ExpectedExecutions: 1,
	})

	require.EqualError(t, err, apiError.InvalidPayload)
	require.Empty(t, repository.imports)
}

func TestApplyLegacyProcessingRunBackfillRequiresLiteralConfirmationBeforeReading(t *testing.T) {
	repository := &legacyBackfillRepository{}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	_, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: "yes", ExpectedStudies: 1, ExpectedExecutions: 1,
	})

	require.EqualError(t, err, apiError.InvalidPayload)
	require.Zero(t, repository.listCalls)
	require.Empty(t, repository.imports)
}

func TestApplyLegacyProcessingRunBackfillImportsStudiesInDeterministicOrder(t *testing.T) {
	repository := &legacyBackfillRepository{rows: []repositoryTypes.LegacyProcessingRunBackfillRow{
		legacyBackfillRow("execution-2", "candidate-2", "tenant-b", "study-z", "model-two", entity.InferenceIngestionProcessingJobStatusCompleted),
		legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-a", "model-one", entity.InferenceIngestionProcessingJobStatusQueued),
	}}
	repository.importFn = func(repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error) {
		return repositoryTypes.ImportLegacyProcessingRunResult{LinkedExecutions: 1}, nil
	}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillConfirmation, ExpectedStudies: 2, ExpectedExecutions: 2,
	})

	require.NoError(t, err)
	require.Equal(t, 2, result.ImportedStudies)
	require.Equal(t, 2, result.ImportedExecutions)
	require.Equal(t, map[string]int{serviceTypes.LegacyBackfillOutcomeImported: 2}, result.Outcomes)
	require.Len(t, repository.imports, 2)
	require.Equal(t, "tenant-a", repository.imports[0].TenantID)
	require.Equal(t, "study-a", repository.imports[0].StudyInstanceUID)
	require.NotEmpty(t, repository.imports[0].RunID)
	require.Equal(t, "tenant-b", repository.imports[1].TenantID)
}

func TestApplyLegacyProcessingRunBackfillStopsAfterUnexpectedImportFailure(t *testing.T) {
	repository := &legacyBackfillRepository{rows: []repositoryTypes.LegacyProcessingRunBackfillRow{
		legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-a", "model-one", entity.InferenceIngestionProcessingJobStatusCompleted),
		legacyBackfillRow("execution-2", "candidate-2", "tenant-a", "study-b", "model-two", entity.InferenceIngestionProcessingJobStatusCompleted),
		legacyBackfillRow("execution-3", "candidate-3", "tenant-a", "study-c", "model-three", entity.InferenceIngestionProcessingJobStatusCompleted),
	}}
	repository.importFn = func(data repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error) {
		if data.StudyInstanceUID == "study-b" {
			return repositoryTypes.ImportLegacyProcessingRunResult{}, errors.New(apiError.DatabaseError)
		}
		return repositoryTypes.ImportLegacyProcessingRunResult{LinkedExecutions: 1}, nil
	}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillConfirmation, ExpectedStudies: 3, ExpectedExecutions: 3,
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.Equal(t, 1, result.ImportedStudies)
	require.Equal(t, map[string]int{
		serviceTypes.LegacyBackfillOutcomeImported: 1,
		serviceTypes.LegacyBackfillOutcomeFailed:   1,
	}, result.Outcomes)
	require.Len(t, repository.imports, 2)
}

func TestApplyLegacyProcessingRunBackfillAcceptsConfirmedConcurrentCompletion(t *testing.T) {
	rows := []repositoryTypes.LegacyProcessingRunBackfillRow{
		legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-a", "model-one", entity.InferenceIngestionProcessingJobStatusCompleted),
	}
	repository := &legacyBackfillRepository{rowResponses: [][]repositoryTypes.LegacyProcessingRunBackfillRow{rows, {}}}
	repository.importFn = func(repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error) {
		return repositoryTypes.ImportLegacyProcessingRunResult{}, errors.New(apiError.DuplicateRecord)
	}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillConfirmation, ExpectedStudies: 1, ExpectedExecutions: 1,
	})

	require.NoError(t, err)
	require.Equal(t, 1, result.AlreadyImportedStudies)
	require.Equal(t, 1, result.AlreadyImportedExecutions)
	require.Equal(t, map[string]int{serviceTypes.LegacyBackfillOutcomeAlreadyDone: 1}, result.Outcomes)
}

func TestApplyLegacyProcessingRunBackfillRejectsConcurrentRunConflict(t *testing.T) {
	rows := []repositoryTypes.LegacyProcessingRunBackfillRow{
		legacyBackfillRow("execution-1", "candidate-1", "tenant-a", "study-a", "model-one", entity.InferenceIngestionProcessingJobStatusCompleted),
	}
	conflict := append([]repositoryTypes.LegacyProcessingRunBackfillRow(nil), rows...)
	conflict[0].ExistingRun = true
	repository := &legacyBackfillRepository{rowResponses: [][]repositoryTypes.LegacyProcessingRunBackfillRow{rows, conflict}}
	repository.importFn = func(repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error) {
		return repositoryTypes.ImportLegacyProcessingRunResult{}, errors.New(apiError.DuplicateRecord)
	}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.ApplyLegacyProcessingRunBackfill(context.Background(), serviceTypes.ApplyLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillConfirmation, ExpectedStudies: 1, ExpectedExecutions: 1,
	})

	require.EqualError(t, err, apiError.DuplicateRecord)
	require.Equal(t, map[string]int{serviceTypes.LegacyBackfillOutcomeFailed: 1}, result.Outcomes)
}
