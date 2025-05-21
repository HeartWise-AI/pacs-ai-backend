package types

// OrchestratorPredictInferenceModel holds the information for predicting an inference model in the orchestrator
type OrchestratorPredictInferenceModel struct {
	StudyInstanceUID   string                 `json:"studyInstanceUID"`
	SeriesInstanceUIDs []string               `json:"seriesInstanceUIDs"`
	AdditionalMetadata map[string]interface{} `json:"additionalMetadata,omitempty"`
	ContainerID        string                 `json:"containerId"`
}

// DicomPayloadAnalysis contains the data extracted from a DICOM payload
type DicomPayloadAnalysis struct {
	StudyInstanceUID   string
	SeriesInstanceUIDs []string
	ContainerID        string
	AdditionalMetadata map[string]interface{}
} 