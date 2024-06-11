package types

type SetTokenSession struct {
	SessionID           string
	TenantID            string
	UserID              string
	Role                string
	ExpireTimeInSeconds uint
}
