package types

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

type GetModelInfoResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		ModelID                       string        `json:"modelId"`
		ModelName                     string        `json:"modelName"`
		Version                       string        `json:"version"`
		DicomTargetLevel              string        `json:"dicomTargetLevel"`
		DicomUploadMin                int           `json:"dicomUploadMin"`
		DicomUploadMax                int           `json:"dicomUploadMax"`
		SupportedDicomModalities      []string      `json:"supportedDicomModalities"`
		SupportedDicomTags            []string      `json:"supportedDicomTags"`
		SupportedAdditionalMetadata   []interface{} `json:"supportedAdditionalMetadata"`
		SupportedOutputModes          []string      `json:"supportedOutputModes"`
		ApproveFeedbackQuestionnaires []interface{} `json:"approveFeedbackQuestionnaires"`
		RejectFeedbackQuestionnaires  []interface{} `json:"rejectFeedbackQuestionnaires"`
		OnboardingModelQuestionnaires []interface{} `json:"onboardingModelQuestionnaires"`
	} `json:"data"`
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
