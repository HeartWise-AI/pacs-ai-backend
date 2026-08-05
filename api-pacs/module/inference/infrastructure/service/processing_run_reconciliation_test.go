package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func TestEvaluateProcessingRunReconciliationUsesStateThresholds(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	config := processingReconciliationConfig{
		PendingStaleAfter: 2 * time.Minute,
		QueuedStaleAfter:  10 * time.Minute,
		RunningStaleAfter: 65 * time.Minute,
		ModelRunningStaleAfter: map[string]time.Duration{
			"SlowModel": 3 * time.Hour,
		},
	}
	executions := []entity.InferenceIngestionProcessingJob{
		processingReconciliationExecution("pending-stale", "ModelA", entity.InferenceIngestionProcessingJobStatusPending, now.Add(-3*time.Minute)),
		processingReconciliationExecution("queued-fresh", "ModelB", entity.InferenceIngestionProcessingJobStatusQueued, now.Add(-9*time.Minute)),
		processingReconciliationExecution("running-stale", "ModelC", entity.InferenceIngestionProcessingJobStatusRunning, now.Add(-70*time.Minute)),
		processingReconciliationExecution("slow-running-fresh", "SlowModel", entity.InferenceIngestionProcessingJobStatusRunning, now.Add(-70*time.Minute)),
		processingReconciliationExecution("completed", "ModelD", entity.InferenceIngestionProcessingJobStatusCompleted, now.Add(-24*time.Hour)),
	}

	evaluation := evaluateProcessingRunReconciliation(
		entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseProcessing},
		executions,
		now,
		config,
	)

	require.True(t, evaluation.Eligible)
	require.Equal(t, []string{"pending-stale", "running-stale"}, []string{
		evaluation.StaleExecutions[0].Execution.ID,
		evaluation.StaleExecutions[1].Execution.ID,
	})
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionPendingStale, evaluation.StaleExecutions[0].ReasonCode)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionProcessingStale, evaluation.StaleExecutions[1].ReasonCode)
	require.Len(t, evaluation.Reasons, 2)
}

func TestEvaluateProcessingRunReconciliationIncludesThresholdBoundary(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	execution := processingReconciliationExecution(
		"queued-boundary", "ModelA", entity.InferenceIngestionProcessingJobStatusQueued, now.Add(-10*time.Minute),
	)

	evaluation := evaluateProcessingRunReconciliation(
		entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseQueued},
		[]entity.InferenceIngestionProcessingJob{execution},
		now,
		processingReconciliationConfig{QueuedStaleAfter: 10 * time.Minute},
	)

	require.True(t, evaluation.Eligible)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionQueueStale, evaluation.StaleExecutions[0].ReasonCode)
}

func TestEvaluateProcessingRunReconciliationKeepsFreshActiveRunIneligible(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	execution := processingReconciliationExecution(
		"pending-fresh", "ModelA", entity.InferenceIngestionProcessingJobStatusPending, now.Add(-time.Minute),
	)

	evaluation := evaluateProcessingRunReconciliation(
		entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseQueued},
		[]entity.InferenceIngestionProcessingJob{execution},
		now,
		processingReconciliationConfig{PendingStaleAfter: 2 * time.Minute},
	)

	require.False(t, evaluation.Eligible)
	require.Empty(t, evaluation.StaleExecutions)
}

func TestEvaluateProcessingRunReconciliationIncludesActiveAttentionRun(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	execution := processingReconciliationExecution(
		"pending-fresh", "ModelA", entity.InferenceIngestionProcessingJobStatusPending, now.Add(-time.Minute),
	)
	run := entity.InferenceIngestionProcessingRun{
		Phase:             entity.InferenceIngestionProcessingRunPhaseQueued,
		AttentionRequired: true,
		AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
			Code: entity.InferenceIngestionProcessingRunAttentionDispatchFailed,
		}},
	}

	evaluation := evaluateProcessingRunReconciliation(
		run,
		[]entity.InferenceIngestionProcessingJob{execution},
		now,
		processingReconciliationConfig{PendingStaleAfter: 2 * time.Minute},
	)

	require.True(t, evaluation.Eligible)
	require.Empty(t, evaluation.StaleExecutions)
	require.Equal(t, run.AttentionReasons, evaluation.Reasons)
}

func TestEvaluateProcessingRunReconciliationRejectsTerminalRunWithAttention(t *testing.T) {
	run := entity.InferenceIngestionProcessingRun{
		Phase:             entity.InferenceIngestionProcessingRunPhaseTerminal,
		AttentionRequired: true,
		AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
			Code: entity.InferenceIngestionProcessingRunAttentionReconciliationFailed,
		}},
	}

	evaluation := evaluateProcessingRunReconciliation(
		run,
		[]entity.InferenceIngestionProcessingJob{{Status: entity.InferenceIngestionProcessingJobStatusCompleted}},
		time.Now(),
		processingReconciliationConfig{},
	)

	require.False(t, evaluation.Eligible)
	require.Empty(t, evaluation.Reasons)
	require.Empty(t, evaluation.StaleExecutions)
}

func TestEvaluateProcessingRunReconciliationDetectsEmptyPlanAndInvalidState(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	run := entity.InferenceIngestionProcessingRun{Phase: entity.InferenceIngestionProcessingRunPhaseQueued}

	emptyPlan := evaluateProcessingRunReconciliation(run, nil, now, processingReconciliationConfig{})
	require.True(t, emptyPlan.Eligible)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionEmptyModelPlan, emptyPlan.Reasons[0].Code)

	invalidState := evaluateProcessingRunReconciliation(
		run,
		[]entity.InferenceIngestionProcessingJob{{ID: "invalid", Status: "unknown", UpdatedAt: now}},
		now,
		processingReconciliationConfig{},
	)
	require.True(t, invalidState.Eligible)
	require.Equal(t, entity.InferenceIngestionProcessingRunAttentionStateConflict, invalidState.Reasons[0].Code)
	require.Equal(t, "invalid", invalidState.StaleExecutions[0].Execution.ID)
}

func processingReconciliationExecution(
	id string,
	modelName string,
	status entity.InferenceIngestionProcessingJobStatus,
	updatedAt time.Time,
) entity.InferenceIngestionProcessingJob {
	return entity.InferenceIngestionProcessingJob{
		ID: id, ModelName: modelName, Status: status, UpdatedAt: updatedAt,
	}
}
