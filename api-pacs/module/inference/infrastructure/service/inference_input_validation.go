package service

import (
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/internal/inputlimits"
	"api-pacs/module/inference/infrastructure/service/types"
)

const (
	defaultInferenceMaxSeriesUIDs      = 256
	defaultInferenceMaxMetadataBytes   = 64 * 1024
	defaultInferenceMaxMetadataDepth   = 8
	defaultInferenceMaxMetadataEntries = 256
)

type inferenceInputLimits struct {
	MaxSeriesUIDs      int
	MaxMetadataBytes   int
	MaxMetadataDepth   int
	MaxMetadataEntries int
}

func configuredInferenceInputLimits() inferenceInputLimits {
	return inferenceInputLimits{
		MaxSeriesUIDs:      configuredInferenceInputPositiveInt("INFERENCE_MAX_SERIES_UIDS", defaultInferenceMaxSeriesUIDs),
		MaxMetadataBytes:   configuredInferenceInputPositiveInt("INFERENCE_MAX_METADATA_BYTES", defaultInferenceMaxMetadataBytes),
		MaxMetadataDepth:   configuredInferenceInputPositiveInt("INFERENCE_MAX_METADATA_DEPTH", defaultInferenceMaxMetadataDepth),
		MaxMetadataEntries: configuredInferenceInputPositiveInt("INFERENCE_MAX_METADATA_ENTRIES", defaultInferenceMaxMetadataEntries),
	}
}

func configuredInferenceInputPositiveInt(name string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func validateBoundedPredictInput(data types.PredictInferenceModel, limits inferenceInputLimits) error {
	if !inputlimits.ValidDICOMUID(data.StudyInstanceUID) || len(data.SeriesInstanceUIDs) == 0 {
		return errors.New(apiError.InferenceInputInvalid)
	}
	if len(data.SeriesInstanceUIDs) > limits.MaxSeriesUIDs {
		return &apiError.InferenceModelInputError{
			ErrorCode: apiError.InferenceInputInvalid,
			Maximum:   limits.MaxSeriesUIDs,
			Actual:    len(data.SeriesInstanceUIDs),
		}
	}

	seen := make(map[string]struct{}, len(data.SeriesInstanceUIDs))
	for _, uid := range data.SeriesInstanceUIDs {
		if !inputlimits.ValidDICOMUID(uid) {
			return errors.New(apiError.InferenceInputInvalid)
		}
		if _, duplicate := seen[uid]; duplicate {
			return errors.New(apiError.InferenceInputInvalid)
		}
		seen[uid] = struct{}{}
	}

	if data.AdditionalMetadata == nil {
		return nil
	}
	if err := inputlimits.ValidateJSONValue(
		data.AdditionalMetadata,
		limits.MaxMetadataBytes,
		limits.MaxMetadataDepth,
		limits.MaxMetadataEntries,
	); err != nil {
		return errors.New(apiError.InferenceInputInvalid)
	}
	return nil
}

func validateModelSeriesBounds(modelInfo dockerInferenceTypes.GetModelInfoResponse, actual int) error {
	minimum := modelInfo.Data.DicomUploadMin
	maximum := modelInfo.Data.DicomUploadMax
	if minimum <= 0 || maximum <= 0 || minimum > maximum {
		return errors.New(apiError.InferenceModelConfigurationInvalid)
	}
	if actual < minimum || actual > maximum {
		return &apiError.InferenceModelInputError{
			ErrorCode: apiError.InferenceModelInputOutOfRange,
			Minimum:   minimum,
			Maximum:   maximum,
			Actual:    actual,
		}
	}
	return nil
}

func sortedSeriesInstanceUIDs(values []string) []string {
	result := slices.Clone(values)
	slices.SortFunc(result, func(left, right string) int {
		leftComponent := normalizedLastUIDComponent(left)
		rightComponent := normalizedLastUIDComponent(right)
		if len(leftComponent) < len(rightComponent) {
			return -1
		}
		if len(leftComponent) > len(rightComponent) {
			return 1
		}
		return strings.Compare(leftComponent, rightComponent)
	})
	return result
}

func normalizedLastUIDComponent(value string) string {
	parts := strings.Split(value, ".")
	component := strings.TrimLeft(parts[len(parts)-1], "0")
	if component == "" {
		return "0"
	}
	return component
}
