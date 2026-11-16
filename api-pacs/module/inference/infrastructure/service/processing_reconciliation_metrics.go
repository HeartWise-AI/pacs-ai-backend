package service

import "log"

type ProcessingReconciliationCycleMetrics struct {
	Checked    int
	Repaired   int
	Failed     int
	Unresolved int
	// Redispatched counts pending executions whose lost dispatch was started
	// again in this cycle.
	Redispatched int
}

type ProcessingReconciliationMetricsRecorder interface {
	RecordProcessingReconciliationCycle(ProcessingReconciliationCycleMetrics)
}

// LoggingProcessingReconciliationMetricsRecorder emits one stable structured
// summary per cycle for the existing log-based operational pipeline.
type LoggingProcessingReconciliationMetricsRecorder struct{}

func (*LoggingProcessingReconciliationMetricsRecorder) RecordProcessingReconciliationCycle(
	metrics ProcessingReconciliationCycleMetrics,
) {
	log.Printf("[Ingestion reconciliation metrics] checked=%d repaired=%d failed=%d unresolved=%d redispatched=%d",
		metrics.Checked,
		metrics.Repaired,
		metrics.Failed,
		metrics.Unresolved,
		metrics.Redispatched,
	)
}
