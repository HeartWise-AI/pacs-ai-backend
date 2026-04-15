package entity

// SignedConsent holds the signed consent entity fields
type SignedConsent struct {
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	Timestamp  uint   `json:"timestamp"`
}

// GetModelName returns the model name of signed consent entity that can be used for naming schemas
func (entity *SignedConsent) GetModelName() string {
	return "signed_consents"
}
