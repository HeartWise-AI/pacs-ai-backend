package types

import "time"

type LoginTenantUser struct {
	TenantID       string
	Email          string
	Password       string
	TurnstileToken string
	ClientIP       string
}

type LoginAbuseSignals struct {
	TenantID string
	Email    string
	ClientIP string
}

type LoginProtectionDecision struct {
	ChallengeRequired bool
	RetryAfter        time.Duration
}

type SetTokenSession struct {
	SessionID           string
	TenantID            string
	UserID              string
	Role                string
	ExpireTimeInSeconds uint
}
