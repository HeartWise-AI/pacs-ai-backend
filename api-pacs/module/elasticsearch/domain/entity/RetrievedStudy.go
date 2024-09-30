package entity

// RetrievedStudy holds the study entity fields
type RetrievedStudy struct {
	TenantID         string `json:"tenant_id"`
	TenantName       string `json:"tenant_name"`
	ModalityID       string `json:"modality_id"`
	UserID           string `json:"user_id"`
	Email            string `json:"email"`
	Name             string `json:"name"`
	StudyInstanceUID string `json:"study_instance_uid"`
	Timestamp        uint   `json:"timestamp"`
}

// GetModelName returns the model name of retrieved study entity that can be used for naming schemas
func (entity *RetrievedStudy) GetModelName() string {
	return "retrieved_studies"
}
