package interfaces

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfiguredProcessingRunIDRequirementIsSafeByDefaultAndExplicitlyEnabled(t *testing.T) {
	t.Setenv("INFERENCE_REQUIRE_PROCESSING_RUN_ID", "")
	require.False(t, configuredProcessingRunIDRequirement())
	t.Setenv("INFERENCE_REQUIRE_PROCESSING_RUN_ID", "invalid")
	require.False(t, configuredProcessingRunIDRequirement())
	t.Setenv("INFERENCE_REQUIRE_PROCESSING_RUN_ID", "true")
	require.True(t, configuredProcessingRunIDRequirement())
}
