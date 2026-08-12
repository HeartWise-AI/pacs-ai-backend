package entity

const (
	AccountAccessActive    string = "ACTIVE"
	AccountAccessSuspended string = "SUSPENDED"
)

// User holds the user entity fields
type User struct {
	ID                string `firestore:"id,omitempty"` // firebase auth
	TenantID          string `firestore:"tenant_id"`
	Role              string `firestore:"role"`
	AccessState       string `firestore:"access_state"`
	Name              string `firestore:"name,omitempty"`     // firebase auth (DisplayName)
	Email             string `firestore:"email,omitempty"`    // firebase auth
	Password          string `firestore:"password,omitempty"` // firebase auth
	LicenseNo         string `firestore:"license_no"`
	Specialty         string `firestore:"specialty"`
	IsEmailVerified   bool   `firestore:"is_email_verified,omitempty"`   // firebase auth
	IsAccountDisabled bool   `firestore:"is_account_disabled,omitempty"` // firebase auth
	IsConsentSigned   bool   `firestore:"is_consent_signed,omitempty"`
	IsAdminCreated    bool   `firestore:"is_admin_created"`
	CreatedAt         int    `firestore:"created_at,omitempty"`
	UpdatedAt         int    `firestore:"updated_at"`
}

// ResolveAccountAccessState keeps pre-migration profiles compatible while
// preserving any account Firebase had already disabled.
func ResolveAccountAccessState(accessState string, firebaseDisabled bool) string {
	if firebaseDisabled || (accessState != "" && accessState != AccountAccessActive) {
		return AccountAccessSuspended
	}
	return AccountAccessActive
}

// GetModelName returns the model name of user entity that can be used for naming schemas
func (entity *User) GetModelName() string {
	return "users"
}
