package types

type CreateTenantUser struct {
	TenantID  string
	Role      string
	Email     string
	Password  string
	Name      string
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
	CreatedAt         int
	UpdatedAt         int
}

type UpdateTenantUser struct {
	ID        string
	TenantID  string
	Role      string
	Name      string
	LicenseNo string
	Specialty string
	UpdatedAt uint
}

type UpdateTenantUserPassword struct {
	ID          string
	TenantID    string
	NewPassword string
}
