package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfiguredProcessingReconciliationUsesDefaults(t *testing.T) {
	clearProcessingReconciliationEnvironment(t)

	config := configuredProcessingReconciliation()

	require.Equal(t, 2*time.Minute, config.PendingStaleAfter)
	require.Equal(t, 10*time.Minute, config.QueuedStaleAfter)
	require.Equal(t, 65*time.Minute, config.RunningStaleAfter)
	require.Empty(t, config.ModelRunningStaleAfter)
	require.Equal(t, uint(3), config.FailureThreshold)
}

func TestConfiguredProcessingReconciliationUsesSpecificAndModelThresholds(t *testing.T) {
	clearProcessingReconciliationEnvironment(t)
	t.Setenv(reconciliationPendingStaleMinutesEnv, "4")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "12")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "80")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, `{"Echo Prime": 125, " DeepRV ": 45, "ignored": 0}`)
	t.Setenv(reconciliationFailureThresholdEnv, "5")

	config := configuredProcessingReconciliation()

	require.Equal(t, 4*time.Minute, config.PendingStaleAfter)
	require.Equal(t, 12*time.Minute, config.QueuedStaleAfter)
	require.Equal(t, 80*time.Minute, config.RunningStaleAfter)
	require.Equal(t, 125*time.Minute, config.runningStaleAfter("Echo Prime"))
	require.Equal(t, 45*time.Minute, config.runningStaleAfter("DeepRV"))
	require.Equal(t, 80*time.Minute, config.runningStaleAfter("unknown"))
	require.NotContains(t, config.ModelRunningStaleAfter, "ignored")
	require.Equal(t, uint(5), config.FailureThreshold)
}

func TestConfiguredProcessingReconciliationSupportsLegacyStaleThreshold(t *testing.T) {
	clearProcessingReconciliationEnvironment(t)
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "20")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "7")

	config := configuredProcessingReconciliation()

	require.Equal(t, 20*time.Minute, config.PendingStaleAfter)
	require.Equal(t, 7*time.Minute, config.QueuedStaleAfter)
	require.Equal(t, 20*time.Minute, config.RunningStaleAfter)
}

func TestConfiguredProcessingReconciliationRejectsInvalidValues(t *testing.T) {
	clearProcessingReconciliationEnvironment(t)
	t.Setenv(reconciliationPendingStaleMinutesEnv, "0")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "invalid")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "not-json")
	t.Setenv(reconciliationFailureThresholdEnv, "-1")

	config := configuredProcessingReconciliation()

	require.Equal(t, 2*time.Minute, config.PendingStaleAfter)
	require.Equal(t, 10*time.Minute, config.QueuedStaleAfter)
	require.Equal(t, 65*time.Minute, config.RunningStaleAfter)
	require.Empty(t, config.ModelRunningStaleAfter)
	require.Equal(t, uint(3), config.FailureThreshold)
}

func clearProcessingReconciliationEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES",
		reconciliationPendingStaleMinutesEnv,
		reconciliationQueuedStaleMinutesEnv,
		reconciliationRunningStaleMinutesEnv,
		reconciliationModelRunningStaleMinutesEnv,
		reconciliationFailureThresholdEnv,
	} {
		t.Setenv(name, "")
	}
}
