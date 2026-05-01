package service

import (
	"expvar"
	"fmt"
	"strings"
	"time"
)

var (
	studyServiceDispatchAttemptsTotal = expvar.NewMap("study_service_dispatch_attempts_total")
	studyServiceDispatchLatencyBucket = expvar.NewMap("study_service_dispatch_latency_seconds_bucket")
	studyServiceDispatchLatencyCount  = expvar.NewMap("study_service_dispatch_latency_seconds_count")
	studyServiceDispatchLatencySumMS  = expvar.NewMap("study_service_dispatch_latency_milliseconds_sum")
	studyServiceProcessingCallbacks   = expvar.NewMap("study_service_processing_callback_total")
)

var dispatchLatencyBuckets = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

func ObserveStudyServiceDispatchAttempt(outcome string, duration time.Duration) {
	safeOutcome := sanitizeMetricLabelValue(outcome)

	studyServiceDispatchAttemptsTotal.Add(safeOutcome, 1)
	studyServiceDispatchLatencyCount.Add(safeOutcome, 1)
	studyServiceDispatchLatencySumMS.Add(safeOutcome, duration.Milliseconds())

	for _, bucket := range dispatchLatencyBuckets {
		if duration > bucket {
			continue
		}

		studyServiceDispatchLatencyBucket.Add(
			fmt.Sprintf("outcome=%s,le=%g", safeOutcome, bucket.Seconds()),
			1,
		)
		return
	}

	studyServiceDispatchLatencyBucket.Add(
		fmt.Sprintf("outcome=%s,le=+Inf", safeOutcome),
		1,
	)
}

func ObserveStudyServiceProcessingCallback(status, outcome string) {
	studyServiceProcessingCallbacks.Add(
		fmt.Sprintf(
			"status=%s,outcome=%s",
			sanitizeMetricLabelValue(status),
			sanitizeMetricLabelValue(outcome),
		),
		1,
	)
}

func sanitizeMetricLabelValue(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "unknown"
	}

	return strings.ReplaceAll(trimmed, " ", "_")
}
