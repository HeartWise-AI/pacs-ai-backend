package entity

import (
	"fmt"
	"time"
)

const (
	InferenceIngestionProcessingRunAttentionEmptyExpectedPlan     = "EMPTY_EXPECTED_PLAN"
	InferenceIngestionProcessingRunAttentionInvalidExecutionState = "INVALID_EXECUTION_STATE"
)

// InferenceIngestionProcessingRunExecutionCounts summarizes the frozen expected-model plan.
type InferenceIngestionProcessingRunExecutionCounts struct {
	Expected  int
	Pending   int
	Queued    int
	Running   int
	Completed int
	Failed    int
	Skipped   int
	Cancelled int
	Active    int
}

// InferenceIngestionProcessingRunAggregationInput contains everything needed for a pure calculation.
type InferenceIngestionProcessingRunAggregationInput struct {
	Run               InferenceIngestionProcessingRun
	Executions        []InferenceIngestionProcessingJob
	WholeRunCancelled bool
}

// InferenceIngestionProcessingRunAggregation is the authoritative calculated run state.
type InferenceIngestionProcessingRunAggregation struct {
	Phase             InferenceIngestionProcessingRunPhase
	Outcome           *InferenceIngestionProcessingRunOutcome
	Counts            InferenceIngestionProcessingRunExecutionCounts
	AttentionRequired bool
	AttentionReasons  InferenceIngestionProcessingRunAttentionReasons
	StartedAt         *time.Time
	CompletedAt       *time.Time
	NextVersion       int64
}

// AggregateInferenceIngestionProcessingRun calculates aggregate state without persistence or side effects.
func AggregateInferenceIngestionProcessingRun(input InferenceIngestionProcessingRunAggregationInput) InferenceIngestionProcessingRunAggregation {
	counts := InferenceIngestionProcessingRunExecutionCounts{Expected: len(input.Executions)}
	reasons := preservedProcessingRunAttentionReasons(input.Run.AttentionReasons)
	preserveUnstructuredAttention := input.Run.AttentionRequired && len(input.Run.AttentionReasons) == 0
	startedAt := earliestProcessingTimestamp(input.Run.StartedAt, nil)
	latestCompletedAt := latestProcessingTimestamp(input.Run.CompletedAt, nil)
	allTerminal := len(input.Executions) > 0

	if len(input.Executions) == 0 {
		reasons = appendProcessingRunAttentionReason(reasons, InferenceIngestionProcessingRunAttentionReason{
			Code: InferenceIngestionProcessingRunAttentionEmptyExpectedPlan,
		})
	}

	for _, execution := range input.Executions {
		startedAt = earliestProcessingTimestamp(startedAt, execution.StartedAt)
		latestCompletedAt = latestProcessingTimestamp(latestCompletedAt, execution.CompletedAt)

		switch execution.Status {
		case InferenceIngestionProcessingJobStatusPending:
			counts.Pending++
		case InferenceIngestionProcessingJobStatusQueued:
			counts.Queued++
		case InferenceIngestionProcessingJobStatusRunning:
			counts.Running++
		case InferenceIngestionProcessingJobStatusCompleted:
			counts.Completed++
		case InferenceIngestionProcessingJobStatusFailed:
			counts.Failed++
		case InferenceIngestionProcessingJobStatusSkipped:
			counts.Skipped++
		case InferenceIngestionProcessingJobStatusCancelled:
			counts.Cancelled++
		default:
			allTerminal = false
			message := fmt.Sprintf("execution %s has invalid status %q", execution.ID, execution.Status)
			reasons = appendProcessingRunAttentionReason(reasons, InferenceIngestionProcessingRunAttentionReason{
				Code: InferenceIngestionProcessingRunAttentionInvalidExecutionState, Message: &message,
			})
			continue
		}

		if !execution.Status.IsTerminal() {
			allTerminal = false
		}
	}
	counts.Active = counts.Pending + counts.Queued + counts.Running

	phase := InferenceIngestionProcessingRunPhaseQueued
	if allTerminal {
		phase = InferenceIngestionProcessingRunPhaseTerminal
	} else if counts.Running > 0 {
		phase = InferenceIngestionProcessingRunPhaseProcessing
	}

	var outcome *InferenceIngestionProcessingRunOutcome
	var completedAt *time.Time
	if phase == InferenceIngestionProcessingRunPhaseTerminal {
		calculatedOutcome := aggregateProcessingRunOutcome(counts, input.WholeRunCancelled)
		outcome = &calculatedOutcome
		completedAt = latestCompletedAt
	}

	return InferenceIngestionProcessingRunAggregation{
		Phase:             phase,
		Outcome:           outcome,
		Counts:            counts,
		AttentionRequired: preserveUnstructuredAttention || len(reasons) > 0,
		AttentionReasons:  reasons,
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		NextVersion:       input.Run.Version + 1,
	}
}

func aggregateProcessingRunOutcome(counts InferenceIngestionProcessingRunExecutionCounts, wholeRunCancelled bool) InferenceIngestionProcessingRunOutcome {
	if wholeRunCancelled {
		return InferenceIngestionProcessingRunOutcomeCancelled
	}
	if counts.Completed == counts.Expected {
		return InferenceIngestionProcessingRunOutcomeSuccess
	}
	if counts.Completed > 0 && counts.Failed+counts.Cancelled > 0 {
		return InferenceIngestionProcessingRunOutcomePartialSuccess
	}
	if counts.Completed > 0 && counts.Skipped > 0 {
		return InferenceIngestionProcessingRunOutcomeSuccessWithSkips
	}
	if counts.Skipped == counts.Expected {
		return InferenceIngestionProcessingRunOutcomeNoResult
	}
	return InferenceIngestionProcessingRunOutcomeFailed
}

func preservedProcessingRunAttentionReasons(reasons InferenceIngestionProcessingRunAttentionReasons) InferenceIngestionProcessingRunAttentionReasons {
	preserved := make(InferenceIngestionProcessingRunAttentionReasons, 0, len(reasons))
	for _, reason := range reasons {
		if reason.Code == InferenceIngestionProcessingRunAttentionEmptyExpectedPlan ||
			reason.Code == InferenceIngestionProcessingRunAttentionInvalidExecutionState {
			continue
		}
		preserved = appendProcessingRunAttentionReason(preserved, reason)
	}
	return preserved
}

func appendProcessingRunAttentionReason(reasons InferenceIngestionProcessingRunAttentionReasons, reason InferenceIngestionProcessingRunAttentionReason) InferenceIngestionProcessingRunAttentionReasons {
	for _, existing := range reasons {
		if existing.Code == reason.Code && equalOptionalProcessingMessage(existing.Message, reason.Message) {
			return reasons
		}
	}
	return append(reasons, reason)
}

func equalOptionalProcessingMessage(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func earliestProcessingTimestamp(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return cloneProcessingTimestamp(current)
	}
	if current == nil || candidate.Before(*current) {
		return cloneProcessingTimestamp(candidate)
	}
	return cloneProcessingTimestamp(current)
}

func latestProcessingTimestamp(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return cloneProcessingTimestamp(current)
	}
	if current == nil || candidate.After(*current) {
		return cloneProcessingTimestamp(candidate)
	}
	return cloneProcessingTimestamp(current)
}

func cloneProcessingTimestamp(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
