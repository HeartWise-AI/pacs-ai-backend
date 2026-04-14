package entity

// AdminInvite holds the admin invite entity fields
type AdminInvite struct {
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	Email      string `json:"email"`
	Timestamp  uint   `json:"timestamp"`
}

// GetModelName returns the model name of admin invite entity that can be used for naming schemas
func (entity *AdminInvite) GetModelName() string {
	return "admin_invites"
}
