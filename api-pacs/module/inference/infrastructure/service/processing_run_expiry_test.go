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
)

type expiryRunRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	selected     []entity.InferenceIngestionProcessingRun
	listInputs   []repositoryTypes.ListNeverRanAutomaticProcessingRuns
	expireInputs []repositoryTypes.ExpireNeverRanProcessingRun
	results      map[string]repositoryTypes.ExpireNeverRanProcessingRunResult
	errs         map[string]error
	listErr      error
	reconciled   bool
}

func (repository *expiryRunRepository) ListNeverRanAutomaticProcessingRuns(
	_ context.Context, data repositoryTypes.ListNeverRanAutomaticProcessingRuns,
) ([]entity.InferenceIngestionProcessingRun, error) {
	repository.listInputs = append(repository.listInputs, data)
	if len(repository.selected) > data.Limit {
		return repository.selected[:data.Limit], repository.listErr
	}
	return repository.selected, repository.listErr
}

func (repository *expiryRunRepository) ExpireNeverRanProcessingRun(
	_ context.Context, data repositoryTypes.ExpireNeverRanProcessingRun,
) (repositoryTypes.ExpireNeverRanProcessingRunResult, error) {
	repository.expireInputs = append(repository.expireInputs, data)
	return repository.results[data.ID], repository.errs[data.ID]
}

func (repository *expiryRunRepository) ListProcessingRunsForReconciliation(
	context.Context, repositoryTypes.ListInferenceIngestionProcessingRunsForReconciliation,
) ([]entity.InferenceIngestionProcessingRun, error) {
	repository.reconciled = true
	return nil, nil
}

func expiryRun(id string) entity.InferenceIngestionProcessingRun {
	return entity.InferenceIngestionProcessingRun{
		ID: id, TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
	}
}

func TestAutoRunExpiryClosesTheSelectedRunsAndCountsTheOutcome(t *testing.T) {
	outcome := entity.InferenceIngestionProcessingRunOutcomeNoResult
	terminal := expiryRun("run-1")
	terminal.Phase, terminal.Outcome = entity.InferenceIngestionProcessingRunPhaseTerminal, &outcome
	repository := &expiryRunRepository{
		selected: []entity.InferenceIngestionProcessingRun{expiryRun("run-1"), expiryRun("run-2"), expiryRun("run-3"), expiryRun("run-4")},
		results: map[string]repositoryTypes.ExpireNeverRanProcessingRunResult{
			"run-1": {Expired: true, Executions: 2, Run: terminal},
			"run-2": {NotExpiredReason: "an execution is running"},
			"run-4": {Expired: true, Empty: true, Run: terminal},
		},
		errs: map[string]error{"run-3": errors.New(apiError.DatabaseError)},
	}
	service := &InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository, AutoRunExpireAfter: 720 * time.Hour}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	cycle := service.expireNeverRanAutomaticRuns(context.Background(), now)

	require.Equal(t, AutoRunExpiryCycle{Selected: 4, Expired: 2, EmptyClosed: 1, Executions: 2, Unchanged: 1, Failed: 1}, cycle)
	require.Equal(t, []repositoryTypes.ListNeverRanAutomaticProcessingRuns{{
		CreatedBefore: now.Add(-720 * time.Hour), Limit: autoRunExpiryBatchLimit,
	}}, repository.listInputs)
	require.Len(t, repository.expireInputs, 4, "a failed run does not stop the others")
	require.Equal(t, repositoryTypes.ExpireNeverRanProcessingRun{
		ID: "run-1", TenantID: "tenant-a", CreatedBefore: now.Add(-720 * time.Hour),
		Message: "Expired: did not complete within 30 days (automatic)", ExpiredAt: now,
	}, repository.expireInputs[0])
}

func TestAutoRunExpiryIsBoundedPerCycle(t *testing.T) {
	selected := make([]entity.InferenceIngestionProcessingRun, autoRunExpiryBatchLimit+50)
	results := map[string]repositoryTypes.ExpireNeverRanProcessingRunResult{}
	for index := range selected {
		selected[index] = expiryRun("run-" + time.Duration(index).String())
		results[selected[index].ID] = repositoryTypes.ExpireNeverRanProcessingRunResult{Expired: true, Executions: 1}
	}
	repository := &expiryRunRepository{selected: selected, results: results}
	service := &InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository, AutoRunExpireAfter: 48 * time.Hour}
	cycle := service.expireNeverRanAutomaticRuns(context.Background(), time.Now())
	require.Equal(t, autoRunExpiryBatchLimit, cycle.Expired)
	require.Len(t, repository.expireInputs, autoRunExpiryBatchLimit)
	require.Equal(t, "Expired: did not complete within 2 days (automatic)", repository.expireInputs[0].Message)
}

func TestAutoRunExpiryIsDisabledWithoutAnAgeAndSurvivesAListError(t *testing.T) {
	repository := &expiryRunRepository{}
	cycle := (&InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository}).
		expireNeverRanAutomaticRuns(context.Background(), time.Now())
	require.Equal(t, AutoRunExpiryCycle{}, cycle)
	require.Empty(t, repository.listInputs)

	repository = &expiryRunRepository{listErr: errors.New(apiError.DatabaseError)}
	cycle = (&InferenceCommandService{InferenceProcessingRunRepositoryInterface: repository, AutoRunExpireAfter: time.Hour}).
		expireNeverRanAutomaticRuns(context.Background(), time.Now())
	require.Equal(t, AutoRunExpiryCycle{Failed: 1}, cycle)
	require.Empty(t, repository.expireInputs)
}

func TestReconciliationCycleExpiresBeforeReconcilingAndReportsIt(t *testing.T) {
	repository := &expiryRunRepository{
		selected: []entity.InferenceIngestionProcessingRun{expiryRun("run-1")},
		results:  map[string]repositoryTypes.ExpireNeverRanProcessingRunResult{"run-1": {Expired: true, Executions: 1}},
	}
	metrics := &reconciliationMetricsRecorderTestDouble{}
	service := &InferenceCommandService{
		InferenceProcessingRunRepositoryInterface: repository, ProcessingReconciliationMetricsRecorder: metrics,
		AutoRunExpireAfter: 720 * time.Hour,
	}
	require.NoError(t, service.ExecuteInferenceIngestionReconciliationWorker(context.Background()))
	require.True(t, repository.reconciled)
	require.Equal(t, []ProcessingReconciliationCycleMetrics{{Expired: 1}}, metrics.cycles)
}

func TestAutoRunExpireAfterConfiguration(t *testing.T) {
	for value, expected := range map[string]time.Duration{
		"": 720 * time.Hour, "48": 48 * time.Hour, " 72 ": 72 * time.Hour,
		"0": 720 * time.Hour, "-1": 720 * time.Hour, "1.5": 720 * time.Hour, "soon": 720 * time.Hour,
	} {
		t.Setenv(AutoRunExpireAfterEnv, value)
		require.Equal(t, expected, ConfiguredAutoRunExpireAfter(), value)
	}
	require.Equal(t, "Expired: did not complete within 30 days (automatic)", autoRunExpiryMessage(720*time.Hour))
	require.Equal(t, "Expired: did not complete within 36 hours (automatic)", autoRunExpiryMessage(36*time.Hour))
	require.Equal(t, "Expired: did not complete within 24 hours (automatic)", autoRunExpiryMessage(24*time.Hour))
}
