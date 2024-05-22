package entity

const (
	CreateAction string = "CREATE"
	UpdateAction string = "UPDATE"
	DeleteAction string = "DELETE"
)

// AdminMember holds the admin member entity fields
type AdminMember struct {
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	LicenseNo  string `json:"license_no"`
	Specialty  string `json:"specialty"`
	Action     string `json:"action"` // enum
	Timestamp  uint   `json:"timestamp"`
}

// GetModelName returns the model name of admin member entity that can be used for naming schemas
func (entity *AdminMember) GetModelName() string {
	return "admin_members"
}
