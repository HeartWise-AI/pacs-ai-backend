package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
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

type ModelProvenance struct {
	SourceRepository string  `json:"sourceRepository"`
	SourceRevision   *string `json:"sourceRevision"`
	ModelRepository  string  `json:"modelRepository"`
	ModelRevision    string  `json:"modelRevision"`
	WeightsPath      string  `json:"weightsPath"`
	WeightsSHA256    string  `json:"weightsSha256"`
}

var (
	gitRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	sha256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func (provenance *ModelProvenance) UnmarshalJSON(data []byte) error {
	type modelProvenanceAlias ModelProvenance

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	requiredFields := []string{
		"sourceRepository",
		"sourceRevision",
		"modelRepository",
		"modelRevision",
		"weightsPath",
		"weightsSha256",
	}
	allowedFields := make(map[string]struct{}, len(requiredFields))
	for _, field := range requiredFields {
		allowedFields[field] = struct{}{}
		rawValue, present := fields[field]
		if !present {
			return fmt.Errorf("provenance.%s is required", field)
		}
		if field != "sourceRevision" && bytes.Equal(bytes.TrimSpace(rawValue), []byte("null")) {
			return fmt.Errorf("provenance.%s cannot be null", field)
		}
	}
	for field := range fields {
		if _, allowed := allowedFields[field]; !allowed {
			return fmt.Errorf("provenance.%s is not supported", field)
		}
	}

	var decoded modelProvenanceAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*provenance = ModelProvenance(decoded)
	return provenance.Validate()
}

func (provenance ModelProvenance) Validate() error {
	if !repositoryPattern.MatchString(provenance.SourceRepository) {
		return fmt.Errorf("provenance.sourceRepository must use the owner/repository format")
	}
	if provenance.SourceRevision != nil && !gitRevisionPattern.MatchString(*provenance.SourceRevision) {
		return fmt.Errorf("provenance.sourceRevision must be a 40-character lowercase Git SHA")
	}
	if !repositoryPattern.MatchString(provenance.ModelRepository) {
		return fmt.Errorf("provenance.modelRepository must use the owner/repository format")
	}
	if !gitRevisionPattern.MatchString(provenance.ModelRevision) {
		return fmt.Errorf("provenance.modelRevision must be a 40-character lowercase Git SHA")
	}
	if !validWeightsPath(provenance.WeightsPath) {
		return fmt.Errorf("provenance.weightsPath must be a normalized repository-relative POSIX path")
	}
	if !sha256Pattern.MatchString(provenance.WeightsSHA256) {
		return fmt.Errorf("provenance.weightsSha256 must be a 64-character lowercase SHA-256")
	}
	return nil
}

func validWeightsPath(value string) bool {
	if value == "" || strings.Contains(value, `\`) || path.IsAbs(value) || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
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
		rawValue, present := fields[field]
		if !present {
			return fmt.Errorf("resources.%s is required", field)
		}
		if bytes.Equal(bytes.TrimSpace(rawValue), []byte("null")) {
			return fmt.Errorf("resources.%s cannot be null", field)
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
	ModelID                       string           `json:"modelId"`
	ModelName                     string           `json:"modelName"`
	Version                       string           `json:"version"`
	DicomTargetLevel              string           `json:"dicomTargetLevel"`
	DicomUploadMin                int              `json:"dicomUploadMin"`
	DicomUploadMax                int              `json:"dicomUploadMax"`
	SupportedDicomModalities      []string         `json:"supportedDicomModalities"`
	SupportedDicomTags            []string         `json:"supportedDicomTags"`
	SupportedAdditionalMetadata   []interface{}    `json:"supportedAdditionalMetadata"`
	SupportedOutputModes          []string         `json:"supportedOutputModes"`
	ApproveFeedbackQuestionnaires []interface{}    `json:"approveFeedbackQuestionnaires"`
	RejectFeedbackQuestionnaires  []interface{}    `json:"rejectFeedbackQuestionnaires"`
	OnboardingModelQuestionnaires []interface{}    `json:"onboardingModelQuestionnaires"`
	Resources                     ModelResources   `json:"resources"`
	Provenance                    *ModelProvenance `json:"provenance,omitempty"`
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
	if rawProvenance, present := fields["provenance"]; present && bytes.Equal(bytes.TrimSpace(rawProvenance), []byte("null")) {
		return fmt.Errorf("provenance must be omitted or contain a complete object")
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
