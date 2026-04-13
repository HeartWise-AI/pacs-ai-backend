package entity

// User holds the user entity fields
type User struct {
	ID                string `firestore:"id,omitempty"` // firebase auth
	TenantID          string `firestore:"tenant_id"`
	Role              string `firestore:"role"`
	Name              string `firestore:"name,omitempty"`     // firebase auth (DisplayName)
	Email             string `firestore:"email,omitempty"`    // firebase auth
	Password          string `firestore:"password,omitempty"` // firebase auth
	LicenseNo         string `firestore:"license_no"`
	Specialty         string `firestore:"specialty"`
	IsEmailVerified   bool   `firestore:"is_email_verified,omitempty"`   // firebase auth
	IsAccountDisabled bool   `firestore:"is_account_disabled,omitempty"` // firebase auth
	IsConsentSigned   bool   `firestore:"is_consent_signed,omitempty"`
	ExpiresAt         int    `firestore:"expires_at,omitempty"`
	CreatedAt         int    `firestore:"created_at,omitempty"`
	UpdatedAt         int    `firestore:"updated_at"`
}

// GetModelName returns the model name of user entity that can be used for naming schemas
func (entity *User) GetModelName() string {
	return "users"
}
