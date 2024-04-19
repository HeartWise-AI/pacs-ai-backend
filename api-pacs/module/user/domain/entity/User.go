package entity

const (
	// roles
	OwnerRole string = "owner"
	AdminRole string = "admin"
	UserRole  string = "user"
)

// User holds the user entity fields
type User struct {
	ID                string `json:"id,omitempty"` // firebase auth
	TenantID          string `json:"tenant_id"`
	Role              string `json:"role"`
	Name              string `json:"name"`               // firebase auth (DisplayName)
	Email             string `json:"email"`              // firebase auth
	Password          string `json:"password,omitempty"` // firebase auth
	LicenseNo         string `json:"license_no"`
	Specialty         string `json:"specialty"`
	IsEmailVerified   bool   `json:"is_email_verified,omitempty"`   // firebase auth
	IsAccountDisabled bool   `json:"is_account_disabled,omitempty"` // firebase auth
	CreatedAt         uint   `json:"created_at"`
	UpdatedAt         uint   `json:"updated_at"`
}

// GetModelName returns the model name of user entity that can be used for naming schemas
func (entity *User) GetModelName() string {
	return "users"
}
