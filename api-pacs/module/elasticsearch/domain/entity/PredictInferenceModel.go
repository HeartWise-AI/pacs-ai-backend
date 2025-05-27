package entity

// PredictInferenceModel holds the predict inference model entity fields
type PredictInferenceModel struct {
	TenantID           string                 `json:"tenant_id"`
	TenantName         string                 `json:"tenant_name"`
	UserID             string                 `json:"user_id"`
	Email              string                 `json:"email"`
	Name               string                 `json:"name"`
	ContainerID        string                 `json:"container_id"`
	ContainerName      string                 `json:"container_name"`
	InferenceModelID   string                 `json:"inference_model_id"`
	InferenceModelName string                 `json:"inference_model_name"`
	DockerImage        string                 `json:"docker_image"`
	StudyInstanceUID   string                 `json:"study_instance_uid"`
	SeriesInstanceUIDs []string               `json:"series_instance_uids"`
	AdditionalMetadata map[string]interface{} `json:"additional_metadata"`
	Timestamp          uint                   `json:"timestamp"`
}

// GetModelName returns the model name of predict inference model entity that can be used for naming schemas
func (entity *PredictInferenceModel) GetModelName() string {
	return "predict_inference_models"
}
