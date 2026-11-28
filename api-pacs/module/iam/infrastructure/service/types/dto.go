package types

import "time"

type LoginTenantUser struct {
	TenantID       string
	Email          string
	Password       string
	TurnstileToken string
	ClientIP       string
}

type VerifyTenantUserPassword struct {
	TenantID string
	UserID   string
	Email    string
	Password string
	ClientIP string
}

type LoginAbuseSignals struct {
	TenantID string
	Email    string
	ClientIP string
}

type LoginProtectionDecision struct {
	ChallengeRequired bool
	RetryAfter        time.Duration
	// ChallengeRetryAfter is how long until every counter that requires a
	// challenge has expired (bounded to [30s, failure window]). Cooldown mode
	// advertises it as Retry-After instead of asking for a challenge.
	ChallengeRetryAfter time.Duration
}

type SetTokenSession struct {
	SessionID           string
	TenantID            string
	UserID              string
	Role                string
	ExpireTimeInSeconds uint
}
