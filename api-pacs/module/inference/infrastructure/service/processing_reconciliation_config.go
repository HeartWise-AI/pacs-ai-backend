package service

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultReconciliationPendingStaleMinutes  uint = 2
	defaultReconciliationQueuedStaleMinutes   uint = 10
	defaultReconciliationRunningStaleMinutes  uint = 65
	defaultReconciliationFailureThreshold     uint = 3
	reconciliationPendingStaleMinutesEnv           = "INFERENCE_INGESTION_RECONCILIATION_PENDING_MINUTES"
	reconciliationQueuedStaleMinutesEnv            = "INFERENCE_INGESTION_RECONCILIATION_QUEUED_MINUTES"
	reconciliationRunningStaleMinutesEnv           = "INFERENCE_INGESTION_RECONCILIATION_RUNNING_MINUTES"
	reconciliationModelRunningStaleMinutesEnv      = "INFERENCE_INGESTION_RECONCILIATION_MODEL_RUNNING_MINUTES"
	reconciliationFailureThresholdEnv              = "INFERENCE_INGESTION_RECONCILIATION_FAILURE_THRESHOLD"
)

type processingReconciliationConfig struct {
	PendingStaleAfter      time.Duration
	QueuedStaleAfter       time.Duration
	RunningStaleAfter      time.Duration
	ModelRunningStaleAfter map[string]time.Duration
	FailureThreshold       uint
}

func configuredProcessingReconciliation() processingReconciliationConfig {
	legacyFallback := configuredPositiveUint(
		"INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES",
		0,
	)

	pendingFallback := defaultReconciliationPendingStaleMinutes
	queuedFallback := defaultReconciliationQueuedStaleMinutes
	runningFallback := defaultReconciliationRunningStaleMinutes
	if legacyFallback > 0 {
		pendingFallback = legacyFallback
		queuedFallback = legacyFallback
		runningFallback = legacyFallback
	}

	return processingReconciliationConfig{
		PendingStaleAfter: time.Duration(configuredPositiveUint(
			reconciliationPendingStaleMinutesEnv, pendingFallback,
		)) * time.Minute,
		QueuedStaleAfter: time.Duration(configuredPositiveUint(
			reconciliationQueuedStaleMinutesEnv, queuedFallback,
		)) * time.Minute,
		RunningStaleAfter: time.Duration(configuredPositiveUint(
			reconciliationRunningStaleMinutesEnv, runningFallback,
		)) * time.Minute,
		ModelRunningStaleAfter: configuredModelRunningStaleThresholds(),
		FailureThreshold: configuredPositiveUint(
			reconciliationFailureThresholdEnv, defaultReconciliationFailureThreshold,
		),
	}
}

func (config processingReconciliationConfig) runningStaleAfter(modelName string) time.Duration {
	if threshold, ok := config.ModelRunningStaleAfter[strings.TrimSpace(modelName)]; ok {
		return threshold
	}
	return config.RunningStaleAfter
}

func configuredPositiveUint(name string, fallback uint) uint {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		log.Printf("[Ingestion reconciliation worker] invalid %s=%q, using fallback=%d", name, value, fallback)
		return fallback
	}

	return uint(parsed)
}

func configuredModelRunningStaleThresholds() map[string]time.Duration {
	value := strings.TrimSpace(os.Getenv(reconciliationModelRunningStaleMinutesEnv))
	if value == "" {
		return map[string]time.Duration{}
	}

	configured := map[string]uint{}
	if err := json.Unmarshal([]byte(value), &configured); err != nil {
		log.Printf("[Ingestion reconciliation worker] invalid %s JSON, ignoring model overrides: %v",
			reconciliationModelRunningStaleMinutesEnv, err,
		)
		return map[string]time.Duration{}
	}

	thresholds := make(map[string]time.Duration, len(configured))
	for modelName, minutes := range configured {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || minutes == 0 {
			continue
		}
		thresholds[modelName] = time.Duration(minutes) * time.Minute
	}
	return thresholds
}
