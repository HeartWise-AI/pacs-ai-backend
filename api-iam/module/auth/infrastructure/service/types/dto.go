package types

const (
	// claims
	TenantClaim   string = "tenant"
	HospitalClaim string = "hospital"
	RoleClaim     string = "role"

	// roles
	OwnerRole string = "owner"
	AdminRole string = "admin"
	UserRole  string = "user"
)

type AddTenantUser struct {
	TenantID string
	Role     string
	Email    string
	Name     string
}

type GetTenantUser struct {
	UID               string
	Email             string
	Name              string
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
