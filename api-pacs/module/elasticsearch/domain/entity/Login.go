package entity

// Login holds the login entity fields
type Login struct {
	SessionID  string `json:"session_id"`
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Specialty  string `json:"specialty"`
	Timestamp  uint   `json:"timestamp"`
}

// GetModelName returns the model name of login entity that can be used for naming schemas
func (entity *Login) GetModelName() string {
	return "logins"
}
