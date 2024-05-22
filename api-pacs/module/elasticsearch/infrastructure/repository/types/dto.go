package types

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
