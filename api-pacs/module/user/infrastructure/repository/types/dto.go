package types

type CreateUser struct {
	TenantID  string
	Role      string
	Name      string
	Email     string
	Password  string
	LicenseNo string
	Specialty string
}

type UpdateUserPassword struct {
	TenantID    string
	ID          string
	NewPassword string
}
