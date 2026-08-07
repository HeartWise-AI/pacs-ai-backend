package service

import (
	"time"

	"api-pacs/module/inference/domain/entity"
)

type processingExecutionReconciliationTarget struct {
	Execution  entity.InferenceIngestionProcessingJob
	ReasonCode string
}

type processingRunReconciliationEvaluation struct {
	Eligible        bool
	Reasons         entity.InferenceIngestionProcessingRunAttentionReasons
	StaleExecutions []processingExecutionReconciliationTarget
}

func evaluateProcessingRunReconciliation(
	run entity.InferenceIngestionProcessingRun,
	executions []entity.InferenceIngestionProcessingJob,
	now time.Time,
	config processingReconciliationConfig,
) processingRunReconciliationEvaluation {
	evaluation := processingRunReconciliationEvaluation{
		Reasons:         entity.InferenceIngestionProcessingRunAttentionReasons{},
		StaleExecutions: []processingExecutionReconciliationTarget{},
	}

	if run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal {
		return evaluation
	}

	for _, reason := range run.AttentionReasons {
		evaluation.Reasons = appendReconciliationReason(evaluation.Reasons, reason.Code)
	}
	if run.AttentionRequired {
		evaluation.Eligible = true
	}

	if len(executions) == 0 {
		evaluation.Eligible = true
		evaluation.Reasons = appendReconciliationReason(
			evaluation.Reasons,
			entity.InferenceIngestionProcessingRunAttentionEmptyModelPlan,
		)
		return evaluation
	}

	for _, execution := range executions {
		if execution.Status.IsTerminal() {
			continue
		}

		threshold, reasonCode, valid := reconciliationThresholdForExecution(execution, config)
		if !valid {
			evaluation.Eligible = true
			evaluation.Reasons = appendReconciliationReason(
				evaluation.Reasons,
				entity.InferenceIngestionProcessingRunAttentionStateConflict,
			)
			evaluation.StaleExecutions = append(evaluation.StaleExecutions, processingExecutionReconciliationTarget{
				Execution: execution, ReasonCode: entity.InferenceIngestionProcessingRunAttentionStateConflict,
			})
			continue
		}

		stateChangedAt := processingExecutionStateChangedAt(execution, run)
		if stateChangedAt.IsZero() || now.Before(stateChangedAt.Add(threshold)) {
			continue
		}

		evaluation.Eligible = true
		evaluation.Reasons = appendReconciliationReason(evaluation.Reasons, reasonCode)
		evaluation.StaleExecutions = append(evaluation.StaleExecutions, processingExecutionReconciliationTarget{
			Execution: execution, ReasonCode: reasonCode,
		})
	}

	return evaluation
}

func reconciliationThresholdForExecution(
	execution entity.InferenceIngestionProcessingJob,
	config processingReconciliationConfig,
) (time.Duration, string, bool) {
	switch execution.Status {
	case entity.InferenceIngestionProcessingJobStatusPending:
		return config.PendingStaleAfter, entity.InferenceIngestionProcessingRunAttentionPendingStale, true
	case entity.InferenceIngestionProcessingJobStatusQueued:
		return config.QueuedStaleAfter, entity.InferenceIngestionProcessingRunAttentionQueueStale, true
	case entity.InferenceIngestionProcessingJobStatusRunning:
		return config.runningStaleAfter(execution.ModelName), entity.InferenceIngestionProcessingRunAttentionProcessingStale, true
	default:
		return 0, "", false
	}
}

func processingExecutionStateChangedAt(
	execution entity.InferenceIngestionProcessingJob,
	run entity.InferenceIngestionProcessingRun,
) time.Time {
	if !execution.UpdatedAt.IsZero() {
		return execution.UpdatedAt
	}
	if !execution.CreatedAt.IsZero() {
		return execution.CreatedAt
	}
	if !run.UpdatedAt.IsZero() {
		return run.UpdatedAt
	}
	return run.CreatedAt
}

func appendReconciliationReason(
	reasons entity.InferenceIngestionProcessingRunAttentionReasons,
	code string,
) entity.InferenceIngestionProcessingRunAttentionReasons {
	for _, reason := range reasons {
		if reason.Code == code {
			return reasons
		}
	}
	return append(reasons, entity.InferenceIngestionProcessingRunAttentionReason{Code: code})
}
