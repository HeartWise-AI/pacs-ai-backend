package types

type CreateTenantUser struct {
	TenantID  string
	Role      string
	Name      string
	Email     string
	LicenseNo string
	Specialty string
}

type GetTenantUser struct {
	ID                string
	TenantID          string
	Role              string
	Name              string
	Email             string
	LicenseNo         string
	Specialty         string
	IsEmailVerified   bool
	IsAccountDisabled bool
	IsConsentSigned   bool
	IsAdminCreated    bool
	CreatedAt         uint
	UpdatedAt         uint
}

type RegisterTenantUser struct {
	TenantID  string
	Role      string
	Name      string
	Email     string
	Password  string
	LicenseNo string
	Specialty string
	Code      *string
}

type ResetTutorial struct {
	TenantID string
	UserID   string
}

type ResendTenantUserEmailInvite struct {
	ID       string
	TenantID string
}

type SendTenantUserEmailInvite struct {
	TenantID string
	Email    string
}

type UpdateTenantUser struct {
	ID        string
	TenantID  string
	Role      string
	Name      string
	LicenseNo string
	Specialty string
}

type UpdateTenantUserPassword struct {
	TenantID    string
	ID          string
	NewPassword string
}

type UpdateUserMetadata struct {
	UserID   string
	Metadata map[string]interface{}
}
