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
	ActiveUpgradeID     string                    `firestore:"active_upgrade_id,omitempty"`
	DeletionClaimID     string                    `firestore:"deletion_claim_id,omitempty"`
	Envs                []string                  `firestore:"envs"`
	DisallowedDICOMTags []string                  `firestore:"disallowed_dicom_tags"`
	OutputMode          OutputMode                `firestore:"output_mode"`
	CreatedAt           int                       `firestore:"created_at"`
	UpdatedAt           int                       `firestore:"updated_at"`
}

// InferenceModelDeployment identifies the exact container artifact selected
// for a registration. Legacy registrations may not have this snapshot yet.
type InferenceModelDeployment struct {
	ContainerID    string                    `firestore:"container_id" json:"containerId"`
	ImageReference string                    `firestore:"image_reference" json:"imageReference"`
	ImageDigest    string                    `firestore:"image_digest" json:"imageDigest"`
	LocalImageID   string                    `firestore:"local_image_id" json:"localImageId"`
	ModelVersion   string                    `firestore:"model_version" json:"modelVersion"`
	Provenance     *InferenceModelProvenance `firestore:"provenance,omitempty" json:"provenance,omitempty"`
	DeployedAt     int64                     `firestore:"deployed_at" json:"deployedAt"`
}

// InferenceModelProvenance is the durable copy of the runtime provenance
// contract captured when a deployment is activated.
type InferenceModelProvenance struct {
	SourceRepository string `firestore:"source_repository" json:"sourceRepository"`
	SourceRevision   string `firestore:"source_revision" json:"sourceRevision"`
	ModelRepository  string `firestore:"model_repository" json:"modelRepository"`
	ModelRevision    string `firestore:"model_revision" json:"modelRevision"`
	WeightsPath      string `firestore:"weights_path" json:"weightsPath"`
	WeightsSHA256    string `firestore:"weights_sha256" json:"weightsSha256"`
}

// GetModelName returns the model name of inference entity that can be used for naming schemas
func (entity *InferenceModel) GetModelName() string {
	return "inference_models"
}
