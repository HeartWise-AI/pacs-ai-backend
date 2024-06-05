package types

type SetSessionToken struct {
	SessionID           string
	TenantID            string
	UserID              string
	Role                string
	ExpireTimeInSeconds uint
}
