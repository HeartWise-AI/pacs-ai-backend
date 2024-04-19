package types

type AddTenantUser struct {
	TenantID  string
	Role      string
	Email     string
	Name      string
	LicenseNo string
	Specialty string
}

type GetTenantUser struct {
	UID               string
	Email             string
	Name              string
	LicenseNo         string // from firestore
	Specialty         string // from firestore
	IsEmailVerified   bool
	IsAccountDisabled bool
	// claims
	TenantID string
	Role     string
}

type UpdateTenantUserPassword struct {
	TenantID    string
	UID         string
	NewPassword string
}
