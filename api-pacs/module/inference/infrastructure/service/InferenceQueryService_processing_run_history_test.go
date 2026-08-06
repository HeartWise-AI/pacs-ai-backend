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

type worklistHistoryRunRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	historyInput    repositoryTypes.ListInferenceIngestionProcessingRuns
	executionsInput repositoryTypes.ListInferenceIngestionProcessingRunExecutions
	selectTenantID  string
	selectRunID     string
	historyPage     repositoryTypes.InferenceIngestionProcessingRunHistoryPage
	run             entity.InferenceIngestionProcessingRun
	executions      []entity.InferenceIngestionProcessingJob
	historyErr      error
	executionsErr   error
	selectErr       error
}

func (repository *worklistHistoryRunRepository) ListProcessingRunHistoryPage(_ context.Context, data repositoryTypes.ListInferenceIngestionProcessingRuns) (repositoryTypes.InferenceIngestionProcessingRunHistoryPage, error) {
	repository.historyInput = data
	return repository.historyPage, repository.historyErr
}

func (repository *worklistHistoryRunRepository) ListProcessingRunExecutionsByRunIDs(_ context.Context, data repositoryTypes.ListInferenceIngestionProcessingRunExecutions) ([]entity.InferenceIngestionProcessingJob, error) {
	repository.executionsInput = data
	return repository.executions, repository.executionsErr
}

func (repository *worklistHistoryRunRepository) SelectProcessingRun(_ context.Context, tenantID, runID string) (entity.InferenceIngestionProcessingRun, error) {
	repository.selectTenantID = tenantID
	repository.selectRunID = runID
	return repository.run, repository.selectErr
}

func (repository *worklistHistoryRunRepository) ListProcessingRunExecutions(_ context.Context, tenantID, runID string) ([]entity.InferenceIngestionProcessingJob, error) {
	repository.selectTenantID = tenantID
	repository.selectRunID = runID
	return repository.executions, repository.executionsErr
}

func TestGetStudyProcessingRunHistoryGroupsExecutionsAndMapsCounts(t *testing.T) {
	now := time.Now().UTC()
	run2ID := "run-2"
	run1ID := "run-1"
	skipCode := entity.InferenceIngestionProcessingJobSkipReasonNoUsableDICOM
	skipMessage := "no usable echo instances"
	repository := &worklistHistoryRunRepository{
		historyPage: repositoryTypes.InferenceIngestionProcessingRunHistoryPage{
			Runs: []entity.InferenceIngestionProcessingRun{
				{ID: run2ID, TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunNumber: 2, RunTrigger: entity.InferenceIngestionProcessingRunTriggerManualReprocess, Phase: entity.InferenceIngestionProcessingRunPhaseProcessing, Version: 4, CreatedAt: now, UpdatedAt: now},
				{ID: run1ID, TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunNumber: 1, RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto, Phase: entity.InferenceIngestionProcessingRunPhaseTerminal, Version: 3, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
			},
			HasMore: true,
		},
		executions: []entity.InferenceIngestionProcessingJob{
			{ID: "execution-running", ProcessingRunID: &run2ID, ModelName: "EchoPrime", Status: entity.InferenceIngestionProcessingJobStatusRunning, UpdatedAt: now},
			{ID: "execution-completed", ProcessingRunID: &run2ID, ModelName: "ViewClassifier", Status: entity.InferenceIngestionProcessingJobStatusCompleted, UpdatedAt: now},
			{ID: "execution-skipped", ProcessingRunID: &run1ID, ModelName: "EchoPrime", Status: entity.InferenceIngestionProcessingJobStatusSkipped, SkipReasonCode: &skipCode, SkipReasonMessage: &skipMessage, UpdatedAt: now},
			{ID: "legacy-without-run", ModelName: "ignored"},
		},
	}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	page, err := service.GetStudyProcessingRunHistory(context.Background(), serviceTypes.GetStudyProcessingRunHistory{
		TenantID: " tenant-a ", StudyInstanceUID: " 1.2.3 ", Limit: 2, Offset: 4,
	})

	require.NoError(t, err)
	require.Equal(t, repositoryTypes.ListInferenceIngestionProcessingRuns{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Limit: 2, Offset: 4}, repository.historyInput)
	require.Equal(t, repositoryTypes.ListInferenceIngestionProcessingRunExecutions{TenantID: "tenant-a", ProcessingRunIDs: []string{run2ID, run1ID}}, repository.executionsInput)
	require.Equal(t, serviceTypes.WorklistPage{Limit: 2, Offset: 4, HasMore: true}, page.WorklistPage)
	require.Equal(t, []string{run2ID, run1ID}, []string{page.Runs[0].RunID, page.Runs[1].RunID})
	require.Equal(t, serviceTypes.ProcessingRunCounts{Expected: 2, Running: 1, Completed: 1, Active: 1}, page.Runs[0].ProcessingRunCounts)
	require.Equal(t, serviceTypes.ProcessingRunCounts{Expected: 1, Skipped: 1}, page.Runs[1].ProcessingRunCounts)
	require.Equal(t, skipCode, page.Runs[1].Executions[0].SkipReason.Code)
}

func TestGetStudyProcessingRunHistoryReturnsEmptyStablePage(t *testing.T) {
	repository := &worklistHistoryRunRepository{historyPage: repositoryTypes.InferenceIngestionProcessingRunHistoryPage{Runs: []entity.InferenceIngestionProcessingRun{}}}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	page, err := service.GetStudyProcessingRunHistory(context.Background(), serviceTypes.GetStudyProcessingRunHistory{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Equal(t, defaultWorklistPageLimit, page.Limit)
	require.NotNil(t, page.Runs)
	require.Empty(t, page.Runs)
	require.Empty(t, repository.executionsInput.ProcessingRunIDs)
}

func TestGetProcessingRunDetailUsesTenantForBothReads(t *testing.T) {
	now := time.Now().UTC()
	runID := "run-7"
	pythonJobID := "must-not-be-exposed"
	repository := &worklistHistoryRunRepository{
		run: entity.InferenceIngestionProcessingRun{
			ID: runID, TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunNumber: 7,
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
			Phase:      entity.InferenceIngestionProcessingRunPhaseProcessing, Version: 9, CreatedAt: now, UpdatedAt: now,
		},
		executions: []entity.InferenceIngestionProcessingJob{{
			ID: "execution-1", ProcessingRunID: &runID, ModelName: "EchoPrime",
			Status: entity.InferenceIngestionProcessingJobStatusQueued, StudyServiceJobID: &pythonJobID, UpdatedAt: now,
		}},
	}
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}

	detail, err := service.GetProcessingRunDetail(context.Background(), serviceTypes.GetProcessingRunDetail{
		TenantID: " tenant-a ", RunID: " run-7 ",
	})

	require.NoError(t, err)
	require.Equal(t, "tenant-a", repository.selectTenantID)
	require.Equal(t, runID, repository.selectRunID)
	require.Equal(t, runID, detail.RunID)
	require.Equal(t, serviceTypes.ProcessingRunCounts{Expected: 1, Queued: 1, Active: 1}, detail.ProcessingRunCounts)
	require.Equal(t, "execution-1", detail.Executions[0].ExecutionID)
}

func TestProcessingRunHistoryServiceRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "missing tenant", err: callHistoryWithInput(serviceTypes.GetStudyProcessingRunHistory{StudyInstanceUID: "1.2.3"})},
		{name: "missing study", err: callHistoryWithInput(serviceTypes.GetStudyProcessingRunHistory{TenantID: "tenant-a"})},
		{name: "negative offset", err: callHistoryWithInput(serviceTypes.GetStudyProcessingRunHistory{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Offset: -1})},
		{name: "limit over maximum", err: callHistoryWithInput(serviceTypes.GetStudyProcessingRunHistory{TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Limit: maximumWorklistPageLimit + 1})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { require.Error(t, test.err) })
	}
}

func callHistoryWithInput(data serviceTypes.GetStudyProcessingRunHistory) error {
	service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: &worklistHistoryRunRepository{}}
	_, err := service.GetStudyProcessingRunHistory(context.Background(), data)
	return err
}

func TestGetProcessingRunDetailPropagatesMissingAndExecutionFailures(t *testing.T) {
	t.Run("missing run", func(t *testing.T) {
		repository := &worklistHistoryRunRepository{selectErr: errors.New(apiError.MissingRecord)}
		service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}
		_, err := service.GetProcessingRunDetail(context.Background(), serviceTypes.GetProcessingRunDetail{TenantID: "tenant-a", RunID: "missing"})
		require.EqualError(t, err, apiError.MissingRecord)
	})

	t.Run("execution query", func(t *testing.T) {
		repository := &worklistHistoryRunRepository{run: entity.InferenceIngestionProcessingRun{ID: "run-1"}, executionsErr: errors.New(apiError.DatabaseError)}
		service := InferenceQueryService{InferenceProcessingRunRepositoryInterface: repository}
		_, err := service.GetProcessingRunDetail(context.Background(), serviceTypes.GetProcessingRunDetail{TenantID: "tenant-a", RunID: "run-1"})
		require.EqualError(t, err, apiError.DatabaseError)
	})
}
