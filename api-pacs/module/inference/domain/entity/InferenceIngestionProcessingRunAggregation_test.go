package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func processingExecution(id string, status InferenceIngestionProcessingJobStatus) InferenceIngestionProcessingJob {
	return InferenceIngestionProcessingJob{ID: id, Status: status}
}

func processingOutcome(value InferenceIngestionProcessingRunOutcome) *InferenceIngestionProcessingRunOutcome {
	return &value
}

func TestAggregateInferenceIngestionProcessingRunOutcomeMatrix(t *testing.T) {
	tests := []struct {
		name               string
		statuses           []InferenceIngestionProcessingJobStatus
		wholeRunCancelled  bool
		expectedPhase      InferenceIngestionProcessingRunPhase
		expectedOutcome    *InferenceIngestionProcessingRunOutcome
		expectedAttention  bool
		expectedReasonCode string
	}{
		{
			name: "pending plan remains queued", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusPending, InferenceIngestionProcessingJobStatusQueued,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseQueued,
		},
		{
			name: "a running execution makes the run processing", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusRunning, InferenceIngestionProcessingJobStatusQueued,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseProcessing,
		},
		{
			name: "all completed is success", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusCompleted, InferenceIngestionProcessingJobStatusCompleted,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomeSuccess),
		},
		{
			name: "completed and skipped is success with skips", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusCompleted, InferenceIngestionProcessingJobStatusSkipped,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomeSuccessWithSkips),
		},
		{
			name: "completed and failed is partial success", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusCompleted, InferenceIngestionProcessingJobStatusFailed,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomePartialSuccess),
		},
		{
			name: "completed and cancelled is partial success", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusCompleted, InferenceIngestionProcessingJobStatusCancelled,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomePartialSuccess),
		},
		{
			name: "all skipped is no result", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusSkipped, InferenceIngestionProcessingJobStatusSkipped,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomeNoResult),
		},
		{
			name: "no completion and a failure is failed", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusFailed, InferenceIngestionProcessingJobStatusSkipped,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomeFailed),
		},
		{
			name: "explicit whole run cancellation is cancelled", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusCancelled, InferenceIngestionProcessingJobStatusCancelled,
			}, wholeRunCancelled: true, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomeCancelled),
		},
		{
			name: "cancellations without a whole run command are failed", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatusCancelled, InferenceIngestionProcessingJobStatusCancelled,
			}, expectedPhase: InferenceIngestionProcessingRunPhaseTerminal,
			expectedOutcome: processingOutcome(InferenceIngestionProcessingRunOutcomeFailed),
		},
		{
			name: "empty plan needs attention", expectedPhase: InferenceIngestionProcessingRunPhaseQueued,
			expectedAttention: true, expectedReasonCode: InferenceIngestionProcessingRunAttentionEmptyModelPlan,
		},
		{
			name: "invalid execution status needs attention", statuses: []InferenceIngestionProcessingJobStatus{
				InferenceIngestionProcessingJobStatus("invalid"),
			}, expectedPhase: InferenceIngestionProcessingRunPhaseQueued,
			expectedAttention: true, expectedReasonCode: InferenceIngestionProcessingRunAttentionInvalidExecutionState,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executions := make([]InferenceIngestionProcessingJob, 0, len(test.statuses))
			for index, status := range test.statuses {
				executions = append(executions, processingExecution(string(rune('a'+index)), status))
			}
			result := AggregateInferenceIngestionProcessingRun(InferenceIngestionProcessingRunAggregationInput{
				Run: InferenceIngestionProcessingRun{Version: 4}, Executions: executions,
				WholeRunCancelled: test.wholeRunCancelled,
			})

			require.Equal(t, test.expectedPhase, result.Phase)
			require.Equal(t, test.expectedOutcome, result.Outcome)
			require.Equal(t, len(executions), result.Counts.Expected)
			require.Equal(t, test.expectedAttention, result.AttentionRequired)
			require.Equal(t, int64(5), result.NextVersion)
			if test.expectedReasonCode != "" {
				require.NotEmpty(t, result.AttentionReasons)
				require.Equal(t, test.expectedReasonCode, result.AttentionReasons[0].Code)
			}
		})
	}
}

func TestAggregateInferenceIngestionProcessingRunCountsEveryExecutionState(t *testing.T) {
	statuses := []InferenceIngestionProcessingJobStatus{
		InferenceIngestionProcessingJobStatusPending,
		InferenceIngestionProcessingJobStatusQueued,
		InferenceIngestionProcessingJobStatusRunning,
		InferenceIngestionProcessingJobStatusCompleted,
		InferenceIngestionProcessingJobStatusFailed,
		InferenceIngestionProcessingJobStatusSkipped,
		InferenceIngestionProcessingJobStatusCancelled,
	}
	executions := make([]InferenceIngestionProcessingJob, 0, len(statuses))
	for index, status := range statuses {
		executions = append(executions, processingExecution(string(rune('a'+index)), status))
	}

	result := AggregateInferenceIngestionProcessingRun(InferenceIngestionProcessingRunAggregationInput{Executions: executions})

	require.Equal(t, InferenceIngestionProcessingRunExecutionCounts{
		Expected: 7, Pending: 1, Queued: 1, Running: 1, Completed: 1,
		Failed: 1, Skipped: 1, Cancelled: 1, Active: 3,
	}, result.Counts)
	require.Equal(t, InferenceIngestionProcessingRunPhaseProcessing, result.Phase)
	require.Nil(t, result.Outcome)
}

func TestAggregateInferenceIngestionProcessingRunCountsAndTimestamps(t *testing.T) {
	startedEarlier := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	startedLater := startedEarlier.Add(2 * time.Minute)
	completedEarlier := startedEarlier.Add(5 * time.Minute)
	completedLater := startedEarlier.Add(7 * time.Minute)
	executions := []InferenceIngestionProcessingJob{
		{ID: "one", Status: InferenceIngestionProcessingJobStatusCompleted, StartedAt: &startedLater, CompletedAt: &completedEarlier},
		{ID: "two", Status: InferenceIngestionProcessingJobStatusFailed, StartedAt: &startedEarlier, CompletedAt: &completedLater},
		{ID: "three", Status: InferenceIngestionProcessingJobStatusSkipped},
	}

	result := AggregateInferenceIngestionProcessingRun(InferenceIngestionProcessingRunAggregationInput{Executions: executions})

	require.Equal(t, 3, result.Counts.Expected)
	require.Equal(t, 1, result.Counts.Completed)
	require.Equal(t, 1, result.Counts.Failed)
	require.Equal(t, 1, result.Counts.Skipped)
	require.Zero(t, result.Counts.Active)
	require.Equal(t, startedEarlier, *result.StartedAt)
	require.Equal(t, completedLater, *result.CompletedAt)
	require.Equal(t, InferenceIngestionProcessingRunOutcomePartialSuccess, *result.Outcome)
}

func TestAggregateInferenceIngestionProcessingRunPreservesOperationalAttention(t *testing.T) {
	operationalReasons := InferenceIngestionProcessingRunAttentionReasons{
		{Code: InferenceIngestionProcessingRunAttentionDispatchFailed},
		{Code: InferenceIngestionProcessingRunAttentionExpectedJobMissing},
		{Code: InferenceIngestionProcessingRunAttentionPendingStale},
		{Code: InferenceIngestionProcessingRunAttentionQueueStale},
		{Code: InferenceIngestionProcessingRunAttentionProcessingStale},
		{Code: InferenceIngestionProcessingRunAttentionCallbackDeadLettered},
		{Code: InferenceIngestionProcessingRunAttentionStudyServiceJobMissing},
		{Code: InferenceIngestionProcessingRunAttentionStateConflict},
		{Code: InferenceIngestionProcessingRunAttentionReconciliationFailed},
	}
	run := InferenceIngestionProcessingRun{
		AttentionRequired: true,
		AttentionReasons:  operationalReasons,
	}

	result := AggregateInferenceIngestionProcessingRun(InferenceIngestionProcessingRunAggregationInput{
		Run: run, Executions: []InferenceIngestionProcessingJob{
			processingExecution("one", InferenceIngestionProcessingJobStatusCompleted),
		},
	})

	require.True(t, result.AttentionRequired)
	require.Equal(t, run.AttentionReasons, result.AttentionReasons)
}

func TestAggregateInferenceIngestionProcessingRunClearsResolvedStructuralAttention(t *testing.T) {
	run := InferenceIngestionProcessingRun{
		AttentionRequired: true,
		AttentionReasons: InferenceIngestionProcessingRunAttentionReasons{{
			Code: InferenceIngestionProcessingRunAttentionEmptyModelPlan,
		}},
	}

	result := AggregateInferenceIngestionProcessingRun(InferenceIngestionProcessingRunAggregationInput{
		Run: run, Executions: []InferenceIngestionProcessingJob{
			processingExecution("one", InferenceIngestionProcessingJobStatusCompleted),
		},
	})

	require.False(t, result.AttentionRequired)
	require.Empty(t, result.AttentionReasons)
}

func TestAggregateInferenceIngestionProcessingRunClearsLegacyEmptyExpectedPlanAttention(t *testing.T) {
	run := InferenceIngestionProcessingRun{
		AttentionRequired: true,
		AttentionReasons: InferenceIngestionProcessingRunAttentionReasons{{
			Code: InferenceIngestionProcessingRunAttentionEmptyExpectedPlan,
		}},
	}

	result := AggregateInferenceIngestionProcessingRun(InferenceIngestionProcessingRunAggregationInput{
		Run: run, Executions: []InferenceIngestionProcessingJob{
			processingExecution("one", InferenceIngestionProcessingJobStatusCompleted),
		},
	})

	require.False(t, result.AttentionRequired)
	require.Empty(t, result.AttentionReasons)
}
