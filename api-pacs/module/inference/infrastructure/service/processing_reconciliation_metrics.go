package service

import "log"

type ProcessingReconciliationCycleMetrics struct {
	Checked    int
	Repaired   int
	Failed     int
	Unresolved int
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
	log.Printf("[Ingestion reconciliation metrics] checked=%d repaired=%d failed=%d unresolved=%d",
		metrics.Checked,
		metrics.Repaired,
		metrics.Failed,
		metrics.Unresolved,
	)
}
