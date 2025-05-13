package entity

// PredictInferenceModel holds the predict inference model entity fields
type PredictInferenceModel struct {
	TenantID         string `json:"tenant_id"`
	TenantName       string `json:"tenant_name"`
	ContainerID      string `json:"container_id"`
	StudyInstanceUID string `json:"study_instance_uid"`
	Timestamp        uint   `json:"timestamp"`
}

// GetModelName returns the model name of predict inference model entity that can be used for naming schemas
func (entity *PredictInferenceModel) GetModelName() string {
	return "predict_inference_models"
}
