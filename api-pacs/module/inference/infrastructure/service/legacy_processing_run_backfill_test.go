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

type legacyBackfillRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	rows                   []repositoryTypes.LegacyProcessingRunBackfillRow
	rowResponses           [][]repositoryTypes.LegacyProcessingRunBackfillRow
	err                    error
	listCalls              int
	imports                []repositoryTypes.ImportLegacyProcessingRun
	importFn               func(repositoryTypes.ImportLegacyProcessingRun) (repositoryTypes.ImportLegacyProcessingRunResult, error)
	runs                   []entity.InferenceIngestionProcessingRun
	verificationExecutions []repositoryTypes.LegacyProcessingRunVerificationExecution
	verificationErr        error
	rollbacks              []repositoryTypes.RollbackLegacyProcessingRun
	rollbackFn             func(repositoryTypes.RollbackLegacyProcessingRun) (repositoryTypes.RollbackLegacyProcessingRunResult, error)
}

func (repository *legacyBackfillRepository) RollbackLegacyProcessingRun(_ context.Context, data repositoryTypes.RollbackLegacyProcessingRun) (repositoryTypes.RollbackLegacyProcessingRunResult, error) {
	repository.rollbacks = append(repository.rollbacks, data)
	if repository.rollbackFn != nil {
		return repository.rollbackFn(data)
	}
	return repositoryTypes.RollbackLegacyProcessingRunResult{UnlinkedExecutions: data.ExpectedExecutions}, nil
}

func (repository *legacyBackfillRepository) LoadLegacyProcessingRunVerificationSnapshot(context.Context) (repositoryTypes.LegacyProcessingRunVerificationSnapshot, error) {
	return repositoryTypes.LegacyProcessingRunVerificationSnapshot{
		Runs: repository.runs, Executions: repository.verificationExecutions, Orphans: repository.rows,
	}, repository.verificationErr
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

func TestVerifyLegacyProcessingRunBackfillPassesCompleteConsistentImport(t *testing.T) {
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	completedAt := now.Add(time.Minute)
	outcome := entity.InferenceIngestionProcessingRunOutcomeSuccess
	runID := "legacy-run-1"
	repository := &legacyBackfillRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: "tenant-a", StudyInstanceUID: "study-a", RunNumber: 1,
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerLegacyImport,
			Phase:      entity.InferenceIngestionProcessingRunPhaseTerminal, Outcome: &outcome,
			Version: 1, StartedAt: &now, CompletedAt: &completedAt, CreatedAt: now, UpdatedAt: completedAt,
		}},
		verificationExecutions: []repositoryTypes.LegacyProcessingRunVerificationExecution{{
			InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{
				ID: "execution-1", ProcessingRunID: &runID, CandidateID: "candidate-1", TenantID: "tenant-a",
				ModelName: "model-one", Status: entity.InferenceIngestionProcessingJobStatusCompleted,
				StartedAt: &now, CompletedAt: &completedAt, CreatedAt: now, UpdatedAt: completedAt,
			},
			CandidateTenantID: "tenant-a", CandidateStudyInstanceUID: "study-a",
		}},
	}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	report, err := service.VerifyLegacyProcessingRunBackfill(context.Background(), serviceTypes.VerifyLegacyProcessingRunBackfill{
		ExpectedStudies: 1, ExpectedExecutions: 1,
	})

	require.NoError(t, err)
	require.True(t, report.Passed)
	require.Equal(t, 1, report.ImportedStudies)
	require.Equal(t, 1, report.ImportedExecutions)
	require.Zero(t, report.InvalidRuns)
	require.Empty(t, report.Issues)
	require.Zero(t, report.Remaining.OrphanExecutions)
}

func TestVerifyLegacyProcessingRunBackfillReportsDriftWithoutIdentifiers(t *testing.T) {
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	runID := "legacy-run-1"
	repository := &legacyBackfillRepository{
		rows: []repositoryTypes.LegacyProcessingRunBackfillRow{
			legacyBackfillRow("orphan-1", "candidate-3", "tenant-a", "study-orphan", "model-three", entity.InferenceIngestionProcessingJobStatusQueued),
		},
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: "tenant-a", StudyInstanceUID: "study-a", RunNumber: 2,
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerLegacyImport,
			Phase:      entity.InferenceIngestionProcessingRunPhaseQueued, Version: 0, CreatedAt: now, UpdatedAt: now,
		}},
		verificationExecutions: []repositoryTypes.LegacyProcessingRunVerificationExecution{
			{
				InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{
					ID: "execution-1", ProcessingRunID: &runID, CandidateID: "candidate-1", TenantID: "tenant-b",
					ModelName: "Model-One", Status: entity.InferenceIngestionProcessingJobStatusCompleted, CreatedAt: now, UpdatedAt: now,
				},
				CandidateTenantID: "tenant-b", CandidateStudyInstanceUID: "study-b",
			},
			{
				InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{
					ID: "execution-2", ProcessingRunID: &runID, CandidateID: "candidate-2", TenantID: "tenant-b",
					ModelName: " model-one ", Status: entity.InferenceIngestionProcessingJobStatusCompleted, CreatedAt: now, UpdatedAt: now,
				},
				CandidateTenantID: "tenant-b", CandidateStudyInstanceUID: "study-b",
			},
		},
	}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	report, err := service.VerifyLegacyProcessingRunBackfill(context.Background(), serviceTypes.VerifyLegacyProcessingRunBackfill{
		ExpectedStudies: 2, ExpectedExecutions: 3,
	})

	require.NoError(t, err)
	require.False(t, report.Passed)
	require.Equal(t, 1, report.InvalidRuns)
	require.Equal(t, 1, report.Remaining.OrphanExecutions)
	for _, issue := range []string{
		serviceTypes.LegacyBackfillVerifyStudyCount,
		serviceTypes.LegacyBackfillVerifyExecutionCount,
		serviceTypes.LegacyBackfillVerifyRemainingOrphans,
		serviceTypes.LegacyBackfillVerifyInvalidRunNumber,
		serviceTypes.LegacyBackfillVerifyInvalidVersion,
		serviceTypes.LegacyBackfillVerifyTenantMismatch,
		serviceTypes.LegacyBackfillVerifyStudyMismatch,
		serviceTypes.LegacyBackfillVerifyDuplicateModel,
		serviceTypes.LegacyBackfillVerifyAggregateMismatch,
	} {
		require.Positive(t, report.Issues[issue], issue)
	}
}

func TestRollbackLegacyProcessingRunBackfillRequiresExactPlanAndRevertsDeterministically(t *testing.T) {
	runA, runB := "legacy-a", "legacy-b"
	repository := &legacyBackfillRepository{
		runs: []entity.InferenceIngestionProcessingRun{
			{ID: runB, TenantID: "tenant-b", StudyInstanceUID: "study-b"},
			{ID: runA, TenantID: "tenant-a", StudyInstanceUID: "study-a"},
		},
		verificationExecutions: []repositoryTypes.LegacyProcessingRunVerificationExecution{
			{InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{ProcessingRunID: &runA}},
			{InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{ProcessingRunID: &runB}},
			{InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{ProcessingRunID: &runB}},
		},
	}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	result, err := service.RollbackLegacyProcessingRunBackfill(context.Background(), serviceTypes.RollbackLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillRollbackConfirmation, ExpectedStudies: 2, ExpectedExecutions: 3,
	})

	require.NoError(t, err)
	require.Equal(t, 2, result.RevertedStudies)
	require.Equal(t, 3, result.RevertedExecutions)
	require.Equal(t, map[string]int{serviceTypes.LegacyBackfillRollbackOutcomeReverted: 2}, result.Outcomes)
	require.Equal(t, []repositoryTypes.RollbackLegacyProcessingRun{
		{RunID: runA, ExpectedExecutions: 1},
		{RunID: runB, ExpectedExecutions: 2},
	}, repository.rollbacks)
}

func TestRollbackLegacyProcessingRunBackfillRejectsStaleCountsBeforeMutation(t *testing.T) {
	runID := "legacy-a"
	repository := &legacyBackfillRepository{
		runs: []entity.InferenceIngestionProcessingRun{{ID: runID}},
		verificationExecutions: []repositoryTypes.LegacyProcessingRunVerificationExecution{
			{InferenceIngestionProcessingJob: entity.InferenceIngestionProcessingJob{ProcessingRunID: &runID}},
		},
	}
	service := InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}

	_, err := service.RollbackLegacyProcessingRunBackfill(context.Background(), serviceTypes.RollbackLegacyProcessingRunBackfill{
		Confirmation: serviceTypes.LegacyBackfillRollbackConfirmation, ExpectedStudies: 2, ExpectedExecutions: 1,
	})

	require.EqualError(t, err, apiError.InvalidPayload)
	require.Empty(t, repository.rollbacks)
}
