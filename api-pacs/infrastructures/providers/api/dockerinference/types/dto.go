package types

import (
	"encoding/json"
	"fmt"
)

type Gender string
type OutputMode string

const (
	// gender
	GenderMale    Gender = "MALE"
	GenderFemale  Gender = "FEMALE"
	GenderOther   Gender = "OTHER"
	GenderUnknown Gender = "UNKNOWN"

	// output mode
	OutputModeJSON            OutputMode = "JSON"
	OutputModeOHIFAnnotations OutputMode = "OHIF_ANNOTATIONS"
	OutputModeHTML            OutputMode = "HTML"
	OutputModeWebApp          OutputMode = "WEB_APP"
	OutputModePDF             OutputMode = "PDF"
)

type PredictRequest struct {
	SeriesInstanceImages   map[int]map[int]string      `json:"seriesInstanceImages,omitempty"`
	SeriesInstanceMetadata map[int]map[int]interface{} `json:"seriesInstanceMetadata,omitempty"`
	AdditionalMetadata     map[string]interface{}      `json:"additionalMetadata,omitempty"`
	OutputMode             OutputMode                  `json:"outputMode"`
}

type ModelResources struct {
	GPURequired             bool `json:"gpuRequired"`
	ResidentMemoryMiB       int  `json:"residentMemoryMiB"`
	PeakMemoryMiB           int  `json:"peakMemoryMiB"`
	MaxConcurrentInferences int  `json:"maxConcurrentInferences"`
	IdleTimeoutSeconds      int  `json:"idleTimeoutSeconds"`
}

func (resources *ModelResources) UnmarshalJSON(data []byte) error {
	type modelResourcesAlias ModelResources

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	for _, field := range []string{
		"gpuRequired",
		"residentMemoryMiB",
		"peakMemoryMiB",
		"maxConcurrentInferences",
		"idleTimeoutSeconds",
	} {
		if _, present := fields[field]; !present {
			return fmt.Errorf("resources.%s is required", field)
		}
	}

	var decoded modelResourcesAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*resources = ModelResources(decoded)
	return resources.Validate()
}

func (resources ModelResources) Validate() error {
	if resources.MaxConcurrentInferences != 1 {
		return fmt.Errorf("resources.maxConcurrentInferences must equal 1 in V1")
	}
	if resources.IdleTimeoutSeconds <= 0 {
		return fmt.Errorf("resources.idleTimeoutSeconds must be greater than 0")
	}

	if resources.GPURequired {
		if resources.ResidentMemoryMiB <= 0 {
			return fmt.Errorf("resources.residentMemoryMiB must be greater than 0 when GPU is required")
		}
		if resources.PeakMemoryMiB <= 0 {
			return fmt.Errorf("resources.peakMemoryMiB must be greater than 0 when GPU is required")
		}
		if resources.PeakMemoryMiB < resources.ResidentMemoryMiB {
			return fmt.Errorf("resources.peakMemoryMiB must be at least resources.residentMemoryMiB")
		}
		return nil
	}

	if resources.ResidentMemoryMiB != 0 || resources.PeakMemoryMiB != 0 {
		return fmt.Errorf("GPU memory fields must equal 0 when resources.gpuRequired is false")
	}
	return nil
}

type ModelInfo struct {
	ModelID                       string         `json:"modelId"`
	ModelName                     string         `json:"modelName"`
	Version                       string         `json:"version"`
	DicomTargetLevel              string         `json:"dicomTargetLevel"`
	DicomUploadMin                int            `json:"dicomUploadMin"`
	DicomUploadMax                int            `json:"dicomUploadMax"`
	SupportedDicomModalities      []string       `json:"supportedDicomModalities"`
	SupportedDicomTags            []string       `json:"supportedDicomTags"`
	SupportedAdditionalMetadata   []interface{}  `json:"supportedAdditionalMetadata"`
	SupportedOutputModes          []string       `json:"supportedOutputModes"`
	ApproveFeedbackQuestionnaires []interface{}  `json:"approveFeedbackQuestionnaires"`
	RejectFeedbackQuestionnaires  []interface{}  `json:"rejectFeedbackQuestionnaires"`
	OnboardingModelQuestionnaires []interface{}  `json:"onboardingModelQuestionnaires"`
	Resources                     ModelResources `json:"resources"`
}

func (modelInfo *ModelInfo) UnmarshalJSON(data []byte) error {
	type modelInfoAlias ModelInfo

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["resources"]; !present {
		return fmt.Errorf("resources is required")
	}

	var decoded modelInfoAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*modelInfo = ModelInfo(decoded)
	return nil
}

type GetModelInfoResponse struct {
	Success bool      `json:"success"`
	Message string    `json:"message"`
	Data    ModelInfo `json:"data"`
}

type GetModelFactsResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		En map[string]interface{} `json:"en"`
	} `json:"data"`
}

type PredictResponse struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data"`
}
