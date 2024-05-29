package entity

// ModalityStudy holds the modality study entity fields
type ModalityStudy struct {
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	TenantAET  string `json:"tenant_aet"`
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	QueryID    string `json:"query_id"`
	Timestamp  uint   `json:"timestamp"`
}

// GetModelName returns the model name of modality study entity that can be used for naming schemas
func (entity *ModalityStudy) GetModelName() string {
	return "find_modality_studies"
}
