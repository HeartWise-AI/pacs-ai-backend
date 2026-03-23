package entity

// StoredCustomSeries holds the stored custom series entity fields
type StoredCustomSeries struct {
	TenantID                string   `json:"tenant_id"`
	TenantName              string   `json:"tenant_name"`
	UserID                  string   `json:"user_id"`
	Email                   string   `json:"email"`
	Name                    string   `json:"name"`
	ModalityID              string   `json:"modality_id"`
	StudyInstanceUID        string   `json:"study_instance_uid"`
	SeriesInstanceUIDs      []string `json:"series_instance_uid"`
	PatientID               string   `json:"patient_id"`
	ModelName               string   `json:"model_name"`
	ModelVersion            string   `json:"model_version"`
	CustomSeriesInstanceUID string   `json:"custom_series_instance_uid"`
	CustomSOPInstanceUID    string   `json:"custom_sop_instance_uid"`
	Timestamp               uint     `json:"timestamp"`
}

// GetModelName returns the model name of stored custom series entity that can be used for naming schemas
func (entity *StoredCustomSeries) GetModelName() string {
	return "stored_custom_series"
}
