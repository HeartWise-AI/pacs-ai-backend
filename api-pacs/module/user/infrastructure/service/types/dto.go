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
	CreatedAt         uint
	UpdatedAt         uint
}

type UpdateTenantUser struct {
	TenantID  string
	UID       string
	LicenseNo string
	Role      string
	Name      string
	Specialty string
}

type UpdateTenantUserPassword struct {
	TenantID    string
	UID         string
	NewPassword string
}
