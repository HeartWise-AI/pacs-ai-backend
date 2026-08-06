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
	rows []repositoryTypes.LegacyProcessingRunBackfillRow
	err  error
}

func (repository *legacyBackfillRepository) ListLegacyProcessingRunBackfillRows(context.Context) ([]repositoryTypes.LegacyProcessingRunBackfillRow, error) {
	return repository.rows, repository.err
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
