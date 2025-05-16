package entity

// DICOMModality holds the DICOM modality entity fields
type DICOMModality struct {
	ID            string `firestore:"id,omitempty"`
	TenantID      string `firestore:"tenant_id"`
	ModalityID    string `firestore:"modality_id"`
	AET           string `firestore:"aet"`
	HostHash      string `firestore:"host_hash"`
	CFindEnabled  bool   `firestore:"c_find_enabled"`
	CMoveEnabled  bool   `firestore:"c_move_enabled"`
	CStoreEnabled bool   `firestore:"c_store_enabled"`
	CreatedAt     int    `firestore:"created_at"`
	UpdatedAt     int    `firestore:"updated_at"`
}

// GetModelName returns the model name of DICOM modality entity that can be used for naming schemas
func (entity *DICOMModality) GetModelName() string {
	return "dicom_modalities"
}
