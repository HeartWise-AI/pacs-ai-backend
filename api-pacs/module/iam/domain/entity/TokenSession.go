package entity

const (
	ExpireTimeInSeconds uint = 900 // 15 mins
)

// TokenSession holds the token session entity fields
type TokenSession struct {
	TenantID string `json:"tenantId"`
	UserID   string `json:"userId"`
	Role     string `json:"role"`
}
