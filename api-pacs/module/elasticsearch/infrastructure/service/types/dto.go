package types

type CreateAdminMemberLog struct {
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	LicenseNo  string
	Specialty  string
	Action     string
}

type CreateLoginLog struct {
	SessionID  string
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	Specialty  string
}

type GetLoginLog struct {
	SessionID  string
	TenantID   string
	TenantName string
	UserID     string
	Email      string
	Name       string
	Role       string
	Specialty  string
	Timestamp  uint
}
