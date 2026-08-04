package entity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
