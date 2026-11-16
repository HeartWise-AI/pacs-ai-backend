package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func waitingDetail(
	t *testing.T,
	run entity.InferenceIngestionProcessingRun,
	executions []entity.InferenceIngestionProcessingJob,
	queryRepository *processingRunPlannerQueryRepository,
) serviceTypes.ProcessingRunDetail {
	t.Helper()
	run.ID, run.TenantID, run.StudyInstanceUID = "run-1", "tenant-a", "1.2.3"
	service := &InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: &worklistHistoryRunRepository{run: run, executions: executions},
	}
	if queryRepository != nil {
		service.InferenceQueryRepositoryInterface = queryRepository
	}
	detail, err := service.GetProcessingRunDetail(context.Background(), serviceTypes.GetProcessingRunDetail{
		TenantID: "tenant-a", RunID: "run-1",
	})
	require.NoError(t, err)
	return detail
}

func waitingExecution(id string, status entity.InferenceIngestionProcessingJobStatus, studyServiceJobID string) entity.InferenceIngestionProcessingJob {
	execution := entity.InferenceIngestionProcessingJob{ID: id, CandidateID: "candidate-" + id, ModelName: "model-" + id, Status: status}
	if studyServiceJobID != "" {
		execution.StudyServiceJobID = &studyServiceJobID
	}
	return execution
}

func TestProcessingRunDetailReportsWhyARunIsWaiting(t *testing.T) {
	lastChangedAt := time.Date(2026, time.November, 16, 10, 0, 0, 0, time.UTC)
	const (
		pending   = entity.InferenceIngestionProcessingJobStatusPending
		queued    = entity.InferenceIngestionProcessingJobStatusQueued
		running   = entity.InferenceIngestionProcessingJobStatusRunning
		completed = entity.InferenceIngestionProcessingJobStatusCompleted
	)
	staleReasons := entity.InferenceIngestionProcessingRunAttentionReasons{
		{Code: entity.InferenceIngestionProcessingRunAttentionPendingStale},
		{Code: entity.InferenceIngestionProcessingRunAttentionExpectedJobMissing},
	}
	candidates := func(statusB, statusC entity.InferenceIngestionCandidateStatus) *processingRunPlannerQueryRepository {
		return &processingRunPlannerQueryRepository{
			candidates: []entity.InferenceIngestionCandidate{
				{ID: "candidate-a", IngestionJobID: "job-a", Status: entity.InferenceIngestionCandidateStatusRetrieved, LastChangedAt: lastChangedAt.Add(-time.Hour)},
				{ID: "candidate-b", IngestionJobID: "job-b", Status: statusB, LastChangedAt: lastChangedAt},
				{ID: "candidate-c", IngestionJobID: "job-c", Status: statusC, LastChangedAt: lastChangedAt.Add(-time.Minute)},
			},
			jobs: map[string]entity.InferenceIngestionJob{
				"job-a": {ID: "job-a", StabilityMinutes: 5},
				"job-b": {ID: "job-b", StabilityMinutes: 20},
				"job-c": {ID: "job-c", StabilityMinutes: 30},
			},
		}
	}
	growing := entity.InferenceIngestionCandidateStatusGrowing
	retrieved := entity.InferenceIngestionCandidateStatusRetrieved
	plan := []entity.InferenceIngestionProcessingJob{
		waitingExecution("a", completed, "job-a"),
		waitingExecution("b", pending, ""),
		waitingExecution("c", pending, ""),
	}
	queuedRun := entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseQueued}

	t.Run("study still receiving images", func(t *testing.T) {
		detail := waitingDetail(t, queuedRun, plan, candidates(growing, growing))

		require.Equal(t, serviceTypes.ProcessingRunWaitingReasonStudyGrowing, detail.WaitingReason)
		// The latest of the growing candidates' stability windows.
		require.NotNil(t, detail.StableAfter)
		require.Equal(t, lastChangedAt.Add(29*time.Minute), *detail.StableAfter)
		require.True(t, detail.Cancellable)
	})

	t.Run("growing study wins over the reconciler's stale flag", func(t *testing.T) {
		run := queuedRun
		run.AttentionRequired, run.AttentionReasons = true, staleReasons

		detail := waitingDetail(t, run, plan, candidates(growing, retrieved))

		require.Equal(t, serviceTypes.ProcessingRunWaitingReasonStudyGrowing, detail.WaitingReason)
		require.Equal(t, lastChangedAt.Add(20*time.Minute), *detail.StableAfter)
	})

	t.Run("never started and flagged stale", func(t *testing.T) {
		run := queuedRun
		run.AttentionRequired, run.AttentionReasons = true, staleReasons

		detail := waitingDetail(t, run, plan, candidates(retrieved, retrieved))

		require.Equal(t, serviceTypes.ProcessingRunWaitingReasonStale, detail.WaitingReason)
		require.Nil(t, detail.StableAfter)
		require.True(t, detail.Cancellable)
	})

	t.Run("stale is reported even when candidates cannot be read", func(t *testing.T) {
		run := queuedRun
		run.AttentionRequired, run.AttentionReasons = true, staleReasons[:1]

		detail := waitingDetail(t, run, plan, &processingRunPlannerQueryRepository{err: errors.New("database unavailable")})

		require.Equal(t, serviceTypes.ProcessingRunWaitingReasonStale, detail.WaitingReason)
	})

	t.Run("merely queued", func(t *testing.T) {
		detail := waitingDetail(t, queuedRun, plan, candidates(retrieved, entity.InferenceIngestionCandidateStatusRetrievalQueued))

		require.Empty(t, detail.WaitingReason)
		require.Nil(t, detail.StableAfter)
		require.True(t, detail.Cancellable)
	})

	t.Run("a model is computing", func(t *testing.T) {
		run := entity.InferenceIngestionProcessingRun{
			Phase: entity.InferenceIngestionProcessingRunPhaseProcessing, AttentionRequired: true, AttentionReasons: staleReasons,
		}
		executions := []entity.InferenceIngestionProcessingJob{
			waitingExecution("a", running, "job-a"), waitingExecution("b", pending, ""),
		}

		detail := waitingDetail(t, run, executions, candidates(growing, growing))

		require.Empty(t, detail.WaitingReason)
		require.True(t, detail.Cancellable)
	})

	t.Run("only models handed to study-service remain", func(t *testing.T) {
		executions := []entity.InferenceIngestionProcessingJob{
			waitingExecution("a", queued, "job-a"), waitingExecution("b", running, "job-b"),
		}

		detail := waitingDetail(t, entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseProcessing}, executions, nil)

		require.False(t, detail.Cancellable)
		require.Empty(t, detail.WaitingReason)
	})

	t.Run("terminal run", func(t *testing.T) {
		outcome := entity.InferenceIngestionProcessingRunOutcomePartialSuccess
		run := entity.InferenceIngestionProcessingRun{
			Phase: entity.InferenceIngestionProcessingRunPhaseTerminal, Outcome: &outcome,
			AttentionRequired: true, AttentionReasons: staleReasons, CancelledAt: &lastChangedAt,
		}
		executions := []entity.InferenceIngestionProcessingJob{
			waitingExecution("a", completed, "job-a"),
			waitingExecution("b", entity.InferenceIngestionProcessingJobStatusCancelled, ""),
		}

		detail := waitingDetail(t, run, executions, candidates(growing, growing))

		require.False(t, detail.Cancellable)
		require.Empty(t, detail.WaitingReason)
		require.Equal(t, lastChangedAt, *detail.CancelledAt)
	})
}

func TestProcessingRunDetailJSONAddsFieldsWithoutChangingExistingOnes(t *testing.T) {
	stableAfter := time.Date(2026, time.November, 16, 10, 20, 0, 0, time.UTC)
	payload, err := json.Marshal(serviceTypes.ProcessingRunDetail{
		ProcessingRunSummary: serviceTypes.ProcessingRunSummary{RunID: "run-1"},
		Executions:           []serviceTypes.ProcessingRunExecutionSummary{},
		Cancellable:          true,
		WaitingReason:        serviceTypes.ProcessingRunWaitingReasonStudyGrowing,
		StableAfter:          &stableAfter,
	})
	require.NoError(t, err)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, "run-1", decoded["runId"])
	require.Equal(t, true, decoded["cancellable"])
	require.Equal(t, "study_growing", decoded["waitingReason"])
	require.Equal(t, "2026-11-16T10:20:00Z", decoded["stableAfter"])
	require.Contains(t, decoded, "expectedModels")
	require.Contains(t, decoded, "executions")

	payload, err = json.Marshal(serviceTypes.ProcessingRunDetail{Executions: []serviceTypes.ProcessingRunExecutionSummary{}})
	require.NoError(t, err)
	decoded = map[string]interface{}{}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, false, decoded["cancellable"])
	for _, absent := range []string{"waitingReason", "stableAfter", "cancelledAt"} {
		require.NotContains(t, decoded, absent)
	}
}
