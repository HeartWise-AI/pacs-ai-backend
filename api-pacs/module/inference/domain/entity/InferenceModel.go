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
	ID          string     `firestore:"id,omitempty"`
	TenantID    string     `firestore:"tenant_id"`
	ContainerID string     `firestore:"container_id"`
	Name        string     `firestore:"name"`
	DockerImage string     `firestore:"docker_image"`
	Envs        []string   `firestore:"envs"`
	OutputMode  OutputMode `firestore:"output_mode"`
	CreatedAt   int        `firestore:"created_at"`
	UpdatedAt   int        `firestore:"updated_at"`
}

// GetModelName returns the model name of inference entity that can be used for naming schemas
func (entity *InferenceModel) GetModelName() string {
	return "inference_models"
}
