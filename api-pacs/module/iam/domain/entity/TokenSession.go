package entity

const (
	ExpireTimeInSeconds uint = 900 // 15 mins

	// SessionGeneration is written into every session, and a session with any
	// other generation is refused. Raising it invalidates every outstanding
	// session; never lower it.
	SessionGeneration = 2
)

// TokenSession holds the token session entity fields
type TokenSession struct {
	TenantID   string `json:"tenantId"`
	UserID     string `json:"userId"`
	Role       string `json:"role"`
	Generation int    `json:"generation,omitempty"`
}
