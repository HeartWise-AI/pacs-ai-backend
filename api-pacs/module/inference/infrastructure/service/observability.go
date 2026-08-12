package service

import (
	"expvar"
	"fmt"
	"strings"
	"time"

	"api-pacs/module/inference/domain/entity"
)

var (
	studyServiceDispatchAttemptsTotal = expvar.NewMap("study_service_dispatch_attempts_total")
	studyServiceDispatchLatencyBucket = expvar.NewMap("study_service_dispatch_latency_seconds_bucket")
	studyServiceDispatchLatencyCount  = expvar.NewMap("study_service_dispatch_latency_seconds_count")
	studyServiceDispatchLatencySumMS  = expvar.NewMap("study_service_dispatch_latency_milliseconds_sum")
	studyServiceProcessingCallbacks   = expvar.NewMap("study_service_processing_callback_total")
	studyServiceCallbackLagBucket     = expvar.NewMap("study_service_processing_callback_lag_seconds_bucket")
	studyServiceCallbackLagCount      = expvar.NewMap("study_service_processing_callback_lag_seconds_count")
	studyServiceCallbackLagSumMS      = expvar.NewMap("study_service_processing_callback_lag_milliseconds_sum")
	processingRunTransitionsTotal     = expvar.NewMap("processing_run_transitions_total")
	processingRunAttentionTotal       = expvar.NewMap("processing_run_attention_total")
	processingRunSkipsTotal           = expvar.NewMap("processing_run_skips_total")
	worklistSSEConnectionsActive      = expvar.NewInt("worklist_sse_connections_active")
	worklistSSEConnectionsTotal       = expvar.NewMap("worklist_sse_connections_total")
	worklistNotificationsTotal        = expvar.NewMap("worklist_notifications_total")
	inferenceQuotaEventsTotal         = expvar.NewMap("inference_quota_events_total")
	inferenceInputRejectionsTotal     = expvar.NewMap("inference_input_rejections_total")
)

var inferenceQuotaEvents = map[string]struct{}{
	"accepted": {}, "rejected_allowance": {}, "rejected_concurrency": {},
	"completed": {}, "released": {}, "refunded": {}, "unavailable": {},
}

var inferenceInputRejectionReasons = map[string]struct{}{
	"invalid_input": {}, "model_bounds": {}, "invalid_model_configuration": {},
}

// ObserveInferenceQuotaEvent records bounded operational outcomes without
// tenant, user, study, model, or reservation identifiers.
func ObserveInferenceQuotaEvent(event string) {
	inferenceQuotaEventsTotal.Add(boundedMetricLabelValue(event, inferenceQuotaEvents), 1)
}

// ObserveInferenceInputRejection records only bounded reason labels; DICOM
// identifiers, metadata, tenant IDs, and model payloads are never metrics.
func ObserveInferenceInputRejection(reason string) {
	inferenceInputRejectionsTotal.Add(boundedMetricLabelValue(reason, inferenceInputRejectionReasons), 1)
}

var dispatchLatencyBuckets = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

var callbackStatuses = map[string]struct{}{
	"pending": {}, "queued": {}, "running": {}, "completed": {},
	"failed": {}, "skipped": {}, "cancelled": {}, "unknown": {},
}

var callbackOutcomes = map[string]struct{}{
	"applied": {}, "ignored": {}, "replayed": {}, "unauthorized": {},
	"invalid_request": {}, "invalid_payload": {}, "not_found": {},
	"error": {}, "unknown": {},
}

var processingAttentionReasons = map[string]struct{}{
	"empty_model_plan":          {},
	"invalid_execution_state":   {},
	"dispatch_failed":           {},
	"expected_job_missing":      {},
	"pending_stale":             {},
	"queue_stale":               {},
	"processing_stale":          {},
	"callback_dead_lettered":    {},
	"study_service_job_missing": {},
	"state_conflict":            {},
	"reconciliation_failed":     {},
	"empty_expected_plan":       {},
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
			boundedMetricLabelValue(status, callbackStatuses),
			boundedMetricLabelValue(outcome, callbackOutcomes),
		),
		1,
	)
}

// ObserveStudyServiceProcessingCallbackLag records transport lag from the
// immutable event occurrence time to Go receipt. Negative clock skew is
// clamped to zero and labels are bounded enums only.
func ObserveStudyServiceProcessingCallbackLag(status, outcome string, occurredAt time.Time) {
	lag := time.Since(occurredAt)
	if lag < 0 {
		lag = 0
	}
	key := fmt.Sprintf(
		"status=%s,outcome=%s",
		boundedMetricLabelValue(status, callbackStatuses),
		boundedMetricLabelValue(outcome, callbackOutcomes),
	)
	studyServiceCallbackLagCount.Add(key, 1)
	studyServiceCallbackLagSumMS.Add(key, lag.Milliseconds())
	observeDurationBucket(studyServiceCallbackLagBucket, key, lag)
}

// ObserveProcessingRunTransition records one committed aggregate transition.
// It deliberately excludes tenant, study, run, model, and result identifiers.
func ObserveProcessingRunTransition(
	run entity.InferenceIngestionProcessingRun,
	execution entity.InferenceIngestionProcessingJob,
	transitionOutcome string,
) {
	outcome := "none"
	if run.Outcome != nil {
		outcome = string(*run.Outcome)
	}
	processingRunTransitionsTotal.Add(fmt.Sprintf(
		"phase=%s,outcome=%s,attention=%t,execution_status=%s,transition=%s",
		boundedProcessingPhase(run.Phase),
		boundedProcessingOutcome(outcome),
		run.AttentionRequired,
		boundedMetricLabelValue(string(execution.Status), callbackStatuses),
		boundedMetricLabelValue(transitionOutcome, callbackOutcomes),
	), 1)
	for _, reason := range run.AttentionReasons {
		processingRunAttentionTotal.Add(boundedMetricLabelValue(reason.Code, processingAttentionReasons), 1)
	}
	if execution.Status == entity.InferenceIngestionProcessingJobStatusSkipped {
		reason := "unknown"
		if skipReason := execution.GetSkipReason(); skipReason != nil {
			reason = strings.ToLower(string(skipReason.Code))
		}
		processingRunSkipsTotal.Add(sanitizeMetricLabelValue(reason), 1)
	}
}

func observeDurationBucket(metric *expvar.Map, key string, duration time.Duration) {
	for _, bucket := range dispatchLatencyBuckets {
		if duration <= bucket {
			metric.Add(fmt.Sprintf("%s,le=%g", key, bucket.Seconds()), 1)
			return
		}
	}
	metric.Add(fmt.Sprintf("%s,le=+Inf", key), 1)
}

func ObserveWorklistSSEConnectionOpened() {
	worklistSSEConnectionsActive.Add(1)
	worklistSSEConnectionsTotal.Add("opened", 1)
}

func ObserveWorklistSSEConnectionClosed() {
	worklistSSEConnectionsActive.Add(-1)
	worklistSSEConnectionsTotal.Add("closed", 1)
}

func ObserveWorklistNotification(outcome string) {
	worklistNotificationsTotal.Add(sanitizeMetricLabelValue(outcome), 1)
}

func sanitizeMetricLabelValue(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "unknown"
	}

	return strings.ReplaceAll(trimmed, " ", "_")
}

func boundedMetricLabelValue(value string, allowed map[string]struct{}) string {
	normalized := sanitizeMetricLabelValue(value)
	if _, ok := allowed[normalized]; !ok {
		return "unknown"
	}
	return normalized
}

func boundedProcessingPhase(phase entity.InferenceIngestionProcessingRunPhase) string {
	switch phase {
	case entity.InferenceIngestionProcessingRunPhaseQueued,
		entity.InferenceIngestionProcessingRunPhaseProcessing,
		entity.InferenceIngestionProcessingRunPhaseTerminal:
		return strings.ToLower(string(phase))
	default:
		return "unknown"
	}
}

func boundedProcessingOutcome(outcome string) string {
	switch entity.InferenceIngestionProcessingRunOutcome(strings.ToUpper(strings.TrimSpace(outcome))) {
	case entity.InferenceIngestionProcessingRunOutcomeSuccess,
		entity.InferenceIngestionProcessingRunOutcomeSuccessWithSkips,
		entity.InferenceIngestionProcessingRunOutcomePartialSuccess,
		entity.InferenceIngestionProcessingRunOutcomeNoResult,
		entity.InferenceIngestionProcessingRunOutcomeFailed,
		entity.InferenceIngestionProcessingRunOutcomeCancelled:
		return strings.ToLower(strings.TrimSpace(outcome))
	default:
		if strings.EqualFold(strings.TrimSpace(outcome), "none") {
			return "none"
		}
		return "unknown"
	}
}
