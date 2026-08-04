package entity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInferenceIngestionProcessingJobStatusTransitionMatrix(t *testing.T) {
	statuses := []InferenceIngestionProcessingJobStatus{
		InferenceIngestionProcessingJobStatusPending,
		InferenceIngestionProcessingJobStatusQueued,
		InferenceIngestionProcessingJobStatusRunning,
		InferenceIngestionProcessingJobStatusCompleted,
		InferenceIngestionProcessingJobStatusFailed,
		InferenceIngestionProcessingJobStatusSkipped,
		InferenceIngestionProcessingJobStatusCancelled,
	}
	allowed := map[InferenceIngestionProcessingJobStatus]map[InferenceIngestionProcessingJobStatus]bool{
		InferenceIngestionProcessingJobStatusPending: {
			InferenceIngestionProcessingJobStatusQueued:    true,
			InferenceIngestionProcessingJobStatusSkipped:   true,
			InferenceIngestionProcessingJobStatusFailed:    true,
			InferenceIngestionProcessingJobStatusCancelled: true,
		},
		InferenceIngestionProcessingJobStatusQueued: {
			InferenceIngestionProcessingJobStatusRunning:   true,
			InferenceIngestionProcessingJobStatusFailed:    true,
			InferenceIngestionProcessingJobStatusSkipped:   true,
			InferenceIngestionProcessingJobStatusCancelled: true,
		},
		InferenceIngestionProcessingJobStatusRunning: {
			InferenceIngestionProcessingJobStatusCompleted: true,
			InferenceIngestionProcessingJobStatusFailed:    true,
			InferenceIngestionProcessingJobStatusSkipped:   true,
			InferenceIngestionProcessingJobStatusCancelled: true,
		},
	}

	for _, current := range statuses {
		for _, next := range statuses {
			t.Run(fmt.Sprintf("%s_to_%s", current, next), func(t *testing.T) {
				expected := current == next || allowed[current][next]
				require.Equal(t, expected, current.CanTransitionTo(next))
			})
		}
	}

	unknown := InferenceIngestionProcessingJobStatus("unknown")
	require.False(t, unknown.CanTransitionTo(unknown))
	require.False(t, InferenceIngestionProcessingJobStatusPending.CanTransitionTo(unknown))
	require.False(t, unknown.CanTransitionTo(InferenceIngestionProcessingJobStatusQueued))
}

func TestInferenceIngestionProcessingJobStatusTerminalStates(t *testing.T) {
	require.False(t, InferenceIngestionProcessingJobStatusPending.IsTerminal())
	require.False(t, InferenceIngestionProcessingJobStatusQueued.IsTerminal())
	require.False(t, InferenceIngestionProcessingJobStatusRunning.IsTerminal())
	require.True(t, InferenceIngestionProcessingJobStatusCompleted.IsTerminal())
	require.True(t, InferenceIngestionProcessingJobStatusFailed.IsTerminal())
	require.True(t, InferenceIngestionProcessingJobStatusSkipped.IsTerminal())
	require.True(t, InferenceIngestionProcessingJobStatusCancelled.IsTerminal())
	require.False(t, InferenceIngestionProcessingJobStatus("unknown").IsTerminal())
}

func TestParseInferenceIngestionProcessingJobStatusNormalizesAllStates(t *testing.T) {
	statuses := []InferenceIngestionProcessingJobStatus{
		InferenceIngestionProcessingJobStatusPending,
		InferenceIngestionProcessingJobStatusQueued,
		InferenceIngestionProcessingJobStatusRunning,
		InferenceIngestionProcessingJobStatusCompleted,
		InferenceIngestionProcessingJobStatusFailed,
		InferenceIngestionProcessingJobStatusSkipped,
		InferenceIngestionProcessingJobStatusCancelled,
	}
	for _, expected := range statuses {
		status, ok := ParseInferenceIngestionProcessingJobStatus("  " + strings.ToUpper(string(expected)) + "  ")
		require.True(t, ok)
		require.Equal(t, expected, status)
	}

	_, ok := ParseInferenceIngestionProcessingJobStatus("not-a-status")
	require.False(t, ok)
}

func TestInferenceIngestionProcessingJobSkipReasonCodesAreValid(t *testing.T) {
	codes := []InferenceIngestionProcessingJobSkipReasonCode{
		InferenceIngestionProcessingJobSkipReasonNoUsableDICOM,
		InferenceIngestionProcessingJobSkipReasonUnsupportedModality,
		InferenceIngestionProcessingJobSkipReasonRequiredSeriesMissing,
		InferenceIngestionProcessingJobSkipReasonModelNotApplicable,
		InferenceIngestionProcessingJobSkipReasonPrerequisiteNotMet,
		InferenceIngestionProcessingJobSkipReasonModelDisabled,
	}

	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			require.True(t, code.IsValid())
		})
	}
	require.False(t, InferenceIngestionProcessingJobSkipReasonCode("UNKNOWN").IsValid())
}

func TestParseInferenceIngestionProcessingJobSkipReasonCodeNormalizesInput(t *testing.T) {
	code, ok := ParseInferenceIngestionProcessingJobSkipReasonCode("  no_usable_dicom ")

	require.True(t, ok)
	require.Equal(t, InferenceIngestionProcessingJobSkipReasonNoUsableDICOM, code)
}

func TestNewInferenceIngestionProcessingJobSkipReasonNormalizesMessage(t *testing.T) {
	message := "  no diagnostic instances remained after preprocessing  "
	reason, err := NewInferenceIngestionProcessingJobSkipReason("required_series_missing", &message)

	require.NoError(t, err)
	require.Equal(t, InferenceIngestionProcessingJobSkipReasonRequiredSeriesMissing, reason.Code)
	require.NotNil(t, reason.Message)
	require.Equal(t, "no diagnostic instances remained after preprocessing", *reason.Message)
}

func TestNewInferenceIngestionProcessingJobSkipReasonRejectsUnknownCode(t *testing.T) {
	_, err := NewInferenceIngestionProcessingJobSkipReason("NOT_A_REASON", nil)

	require.EqualError(t, err, `invalid processing job skip reason code "NOT_A_REASON"`)
}

func TestInferenceIngestionProcessingJobGetSkipReason(t *testing.T) {
	code := InferenceIngestionProcessingJobSkipReasonModelDisabled
	message := "model disabled by tenant configuration"
	job := InferenceIngestionProcessingJob{SkipReasonCode: &code, SkipReasonMessage: &message}

	reason := job.GetSkipReason()
	require.NotNil(t, reason)
	require.Equal(t, code, reason.Code)
	require.Equal(t, message, *reason.Message)

	job.SkipReasonCode = nil
	require.Nil(t, job.GetSkipReason())
}
