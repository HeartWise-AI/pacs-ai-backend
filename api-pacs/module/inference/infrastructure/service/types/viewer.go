package types

import (
	"encoding/json"

	"api-pacs/module/inference/domain/entity"
)

// GetStudyLatestModelResults scopes the viewer's latest-results lookup to the
// authenticated tenant.
type GetStudyLatestModelResults struct {
	TenantID         string
	StudyInstanceUID string
	// Modality optionally filters by study-service modality (e.g. "angiogram").
	Modality string
}

// StudyLatestModelResult joins one study-service latest result with the
// tenant's model registrations and processing runs.
type StudyLatestModelResult struct {
	ModelName    string  `json:"modelName"`
	ModelVersion *string `json:"modelVersion"`
	// Modality is study-service's stored value (e.g. "angiogram"), unchanged.
	Modality string `json:"modality"`
	// ModelID is the tenant's Firestore model registration ID when one matches.
	ModelID *string `json:"modelId"`
	// ArtifactModelID is the model-info modelId (what /model/proxy/available returns as ModelID).
	ArtifactModelID   *string                                        `json:"artifactModelId"`
	ContainerID       *string                                        `json:"containerId"`
	RunID             *string                                        `json:"runId"`
	ExecutionID       *string                                        `json:"executionId"`
	StudyServiceJobID string                                         `json:"studyServiceJobId"`
	CompletedAt       *string                                        `json:"completedAt"`
	Trigger           *entity.InferenceIngestionProcessingRunTrigger `json:"trigger"`
	SelectedViews     json.RawMessage                                `json:"selectedViews"`
	ReportAvailable   bool                                           `json:"reportAvailable"`
}

// StudyLatestModelResults is the viewer latest-results response.
type StudyLatestModelResults struct {
	StudyInstanceUID string                   `json:"studyInstanceUID"`
	Results          []StudyLatestModelResult `json:"results"`
}

// GetProcessingRunExecutionReport scopes a report render to one tenant execution.
type GetProcessingRunExecutionReport struct {
	TenantID    string
	RunID       string
	ExecutionID string
}

// GetStudyJobReport scopes a legacy report render (no run/execution IDs) to
// one tenant study and study-service job.
type GetStudyJobReport struct {
	TenantID          string
	StudyInstanceUID  string
	StudyServiceJobID string
}

// ModelReport is a re-rendered HTML report.
type ModelReport struct {
	HTMLBase64   string `json:"htmlBase64"`
	ContentType  string `json:"contentType"`
	ModelVersion string `json:"modelVersion,omitempty"`
}

// CreateViewerModelRun requests one single-model managed run from the viewer.
type CreateViewerModelRun struct {
	TenantID         string
	UserID           string
	StudyInstanceUID string
	// ModelRef is the Firestore registration ID; the container ID or the
	// model-info modelId are accepted as fallbacks.
	ModelRef string
	// SeriesInstanceUIDs is the series the user picked in the viewer. They are
	// sent to study-service as series_instance_uids and the run uses exactly
	// those; empty lets study-service use every series of the modality.
	SeriesInstanceUIDs []string
	// Modality is an optional DICOM modality (e.g. "XA") for multi-modality models.
	Modality string
}

// CreateViewerModelRunResult describes the created run and its single execution.
type CreateViewerModelRunResult struct {
	Run          entity.InferenceIngestionProcessingRun
	Execution    entity.InferenceIngestionProcessingJob
	ModelID      string
	ModelName    string
	ModelVersion string
}
