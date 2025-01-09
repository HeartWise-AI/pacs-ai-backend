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
	SeriesInstanceMetadata map[int]map[int]interface{} `json:"seriesInstanceMetadata,omitempty"`
	SeriesInstanceImages   map[int]map[int]string      `json:"seriesInstanceImages,omitempty"`
	AdditionalMetadata     map[string]interface{}      `json:"additionalMetadata"`
	OutputMode             OutputMode                  `json:"outputMode"`
}

type GetModelInfoResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		ModelName                   string        `json:"modelName"`
		Version                     string        `json:"version"`
		DicomTargetLevel            string        `json:"dicomTargetLevel"`
		DicomUploadMin              int           `json:"dicomUploadMin"`
		DicomUploadMax              int           `json:"dicomUploadMax"`
		SupportedDicomModalities    []string      `json:"supportedDicomModalities"`
		SupportedDicomTags          []string      `json:"supportedDicomTags"`
		SupportedAdditionalMetadata []interface{} `json:"supportedAdditionalMetadata"`
		SupportedOutputModes        []string      `json:"supportedOutputModes"`
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
