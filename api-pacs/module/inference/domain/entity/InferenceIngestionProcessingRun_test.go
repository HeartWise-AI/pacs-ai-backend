package entity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInferenceIngestionProcessingRunAttentionReasonsRoundTrip(t *testing.T) {
	message := "callback delivery failed"
	original := InferenceIngestionProcessingRunAttentionReasons{
		{Code: "CALLBACK_STALE", Message: &message},
	}

	encoded, err := original.Value()
	require.NoError(t, err)

	var decoded InferenceIngestionProcessingRunAttentionReasons
	require.NoError(t, decoded.Scan(encoded))
	require.Equal(t, original, decoded)
}

func TestInferenceIngestionProcessingRunAttentionReasonsNormalizesNull(t *testing.T) {
	var reasons InferenceIngestionProcessingRunAttentionReasons
	require.NoError(t, reasons.Scan(nil))
	require.Empty(t, reasons)

	encoded, err := reasons.Value()
	require.NoError(t, err)
	require.JSONEq(t, "[]", string(encoded.([]byte)))
}

func TestInferenceIngestionProcessingRunModelName(t *testing.T) {
	run := InferenceIngestionProcessingRun{}
	require.Equal(t, "ingestion_processing_runs", run.GetModelName())
}
