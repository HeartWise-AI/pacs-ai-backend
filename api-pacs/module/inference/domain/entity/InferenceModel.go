package entity

type OutputMode string

const (
	OutputModeJSON            OutputMode = "JSON"
	OutputModeOHIFAnnotations OutputMode = "OHIF_ANNOTATIONS"
	OutputModeHTML            OutputMode = "HTML"
	OutputModeWebApp          OutputMode = "WEB_APP"
	OutputModePDF             OutputMode = "PDF"
)

// InferenceModel holds the inference model entity fields
type InferenceModel struct {
	ID                  string                    `firestore:"id,omitempty"`
	TenantID            string                    `firestore:"tenant_id"`
	ContainerID         string                    `firestore:"container_id"`
	Name                string                    `firestore:"name"`
	DockerImage         string                    `firestore:"docker_image"`
	Deployment          *InferenceModelDeployment `firestore:"deployment,omitempty"`
	Envs                []string                  `firestore:"envs"`
	DisallowedDICOMTags []string                  `firestore:"disallowed_dicom_tags"`
	OutputMode          OutputMode                `firestore:"output_mode"`
	CreatedAt           int                       `firestore:"created_at"`
	UpdatedAt           int                       `firestore:"updated_at"`
}

// InferenceModelDeployment identifies the exact artifact backing a model
// registration. Legacy records may not have this snapshot until upgraded.
type InferenceModelDeployment struct {
	ContainerID      string                    `firestore:"container_id" json:"containerId"`
	CatalogVersionID string                    `firestore:"catalog_version_id,omitempty" json:"catalogVersionId,omitempty"`
	ArtifactModelID  string                    `firestore:"artifact_model_id,omitempty" json:"artifactModelId,omitempty"`
	ImageReference   string                    `firestore:"image_reference" json:"imageReference"`
	ImageDigest      string                    `firestore:"image_digest" json:"imageDigest"`
	LocalImageID     string                    `firestore:"local_image_id" json:"localImageId"`
	ModelVersion     string                    `firestore:"model_version" json:"modelVersion"`
	Provenance       *InferenceModelProvenance `firestore:"provenance,omitempty" json:"provenance,omitempty"`
	DeployedAt       int64                     `firestore:"deployed_at" json:"deployedAt"`
}

// InferenceModelProvenance is the immutable weights and source identity copied
// from the validated candidate's model-info contract.
type InferenceModelProvenance struct {
	SourceRepository string  `firestore:"source_repository" json:"sourceRepository"`
	SourceRevision   *string `firestore:"source_revision" json:"sourceRevision"`
	ModelRepository  string  `firestore:"model_repository" json:"modelRepository"`
	ModelRevision    string  `firestore:"model_revision" json:"modelRevision"`
	WeightsPath      string  `firestore:"weights_path" json:"weightsPath"`
	WeightsSHA256    string  `firestore:"weights_sha256" json:"weightsSha256"`
}

// GetModelName returns the model name of inference entity that can be used for naming schemas
func (entity *InferenceModel) GetModelName() string {
	return "inference_models"
}
