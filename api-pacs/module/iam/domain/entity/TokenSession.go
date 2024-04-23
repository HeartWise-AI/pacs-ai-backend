package entity

// TokenSession holds the token session entity fields
type TokenSession struct {
	TenantID string `json:"tenantId"`
	UserID   string `json:"userId"`
	Role     string `json:"role"`
}

// GetModelName returns the model name of token session entity that can be used for naming schemas
func (entity *TokenSession) GetModelName() string {
	return "token_sessions"
}
