package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func TestValidateBoundedPredictInputAcceptsBoundaryValues(t *testing.T) {
	data := serviceTypes.PredictInferenceModel{
		StudyInstanceUID:   "1.2.3",
		SeriesInstanceUIDs: []string{"1.2.3.1", "1.2.3.2"},
		AdditionalMetadata: map[string]interface{}{"view": "A4C"},
	}
	limits := inferenceInputLimits{MaxSeriesUIDs: 2, MaxMetadataBytes: 32, MaxMetadataDepth: 3, MaxMetadataEntries: 2}

	require.NoError(t, validateBoundedPredictInput(data, limits))
}

func TestValidateBoundedPredictInputRejectsUnsafeVariableInputs(t *testing.T) {
	valid := serviceTypes.PredictInferenceModel{
		StudyInstanceUID:   "1.2.3",
		SeriesInstanceUIDs: []string{"1.2.3.1"},
	}
	testCases := []struct {
		name   string
		mutate func(*serviceTypes.PredictInferenceModel)
		limits inferenceInputLimits
	}{
		{name: "invalid study UID", mutate: func(data *serviceTypes.PredictInferenceModel) { data.StudyInstanceUID = "patient-value" }},
		{name: "invalid series UID", mutate: func(data *serviceTypes.PredictInferenceModel) { data.SeriesInstanceUIDs = []string{"1.2.bad"} }},
		{name: "duplicate series UID", mutate: func(data *serviceTypes.PredictInferenceModel) {
			data.SeriesInstanceUIDs = []string{"1.2.3.1", "1.2.3.1"}
		}},
		{name: "series hard cap", mutate: func(data *serviceTypes.PredictInferenceModel) { data.SeriesInstanceUIDs = []string{"1", "2"} }, limits: inferenceInputLimits{MaxSeriesUIDs: 1}},
		{name: "metadata bytes", mutate: func(data *serviceTypes.PredictInferenceModel) {
			data.AdditionalMetadata = map[string]interface{}{"value": strings.Repeat("x", 40)}
		}, limits: inferenceInputLimits{MaxMetadataBytes: 16}},
		{name: "metadata depth", mutate: func(data *serviceTypes.PredictInferenceModel) {
			data.AdditionalMetadata = map[string]interface{}{"a": map[string]interface{}{"b": "c"}}
		}, limits: inferenceInputLimits{MaxMetadataDepth: 2}},
		{name: "metadata entries", mutate: func(data *serviceTypes.PredictInferenceModel) {
			data.AdditionalMetadata = map[string]interface{}{"a": 1, "b": 2}
		}, limits: inferenceInputLimits{MaxMetadataEntries: 1}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			data := valid
			testCase.mutate(&data)
			limits := testCase.limits
			if limits.MaxSeriesUIDs == 0 {
				limits.MaxSeriesUIDs = 10
			}
			if limits.MaxMetadataBytes == 0 {
				limits.MaxMetadataBytes = 1024
			}
			if limits.MaxMetadataDepth == 0 {
				limits.MaxMetadataDepth = 8
			}
			if limits.MaxMetadataEntries == 0 {
				limits.MaxMetadataEntries = 20
			}

			err := validateBoundedPredictInput(data, limits)
			require.Error(t, err)
			require.Equal(t, apiError.InferenceInputInvalid, err.Error())
		})
	}
}

func TestValidateModelSeriesBoundsUsesAuthoritativeModelContract(t *testing.T) {
	modelInfo := dockerInferenceTypes.GetModelInfoResponse{}
	modelInfo.Data.DicomUploadMin = 2
	modelInfo.Data.DicomUploadMax = 4

	require.NoError(t, validateModelSeriesBounds(modelInfo, 2))
	require.NoError(t, validateModelSeriesBounds(modelInfo, 4))

	for _, actual := range []int{1, 5} {
		err := validateModelSeriesBounds(modelInfo, actual)
		var boundsError *apiError.InferenceModelInputError
		require.ErrorAs(t, err, &boundsError)
		require.Equal(t, apiError.InferenceModelInputOutOfRange, boundsError.ErrorCode)
		require.Equal(t, 2, boundsError.Minimum)
		require.Equal(t, 4, boundsError.Maximum)
		require.Equal(t, actual, boundsError.Actual)
	}
}

func TestValidateModelSeriesBoundsFailsClosedForInvalidModelContract(t *testing.T) {
	modelInfo := dockerInferenceTypes.GetModelInfoResponse{}
	modelInfo.Data.DicomUploadMin = 4
	modelInfo.Data.DicomUploadMax = 2

	err := validateModelSeriesBounds(modelInfo, 3)

	require.EqualError(t, err, apiError.InferenceModelConfigurationInvalid)
}

func TestSortedSeriesInstanceUIDsUsesNumericSuffixAfterValidation(t *testing.T) {
	values := []string{"1.2.10", "1.2.2", "1.2.0003", "1.2.999999999999999999999999999999999999"}

	result := sortedSeriesInstanceUIDs(values)

	require.Equal(t, []string{"1.2.2", "1.2.0003", "1.2.10", "1.2.999999999999999999999999999999999999"}, result)
	require.Equal(t, "1.2.10", values[0], "sorting must not mutate the caller's slice")
}

func TestConfiguredInferenceInputLimitsUsesDefaultsAndOverrides(t *testing.T) {
	t.Setenv("INFERENCE_MAX_SERIES_UIDS", "invalid")
	t.Setenv("INFERENCE_MAX_METADATA_BYTES", "1024")
	t.Setenv("INFERENCE_MAX_METADATA_DEPTH", "4")
	t.Setenv("INFERENCE_MAX_METADATA_ENTRIES", "12")

	limits := configuredInferenceInputLimits()

	require.Equal(t, defaultInferenceMaxSeriesUIDs, limits.MaxSeriesUIDs)
	require.Equal(t, 1024, limits.MaxMetadataBytes)
	require.Equal(t, 4, limits.MaxMetadataDepth)
	require.Equal(t, 12, limits.MaxMetadataEntries)
}
