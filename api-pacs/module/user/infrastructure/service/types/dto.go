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
	ID       string
	UserID   string
	Metadata map[string]interface{}
}
