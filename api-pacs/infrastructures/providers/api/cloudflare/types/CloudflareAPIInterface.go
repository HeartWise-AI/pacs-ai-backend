package types

import "context"

// CloudflareAPIInterface is the interface for the Cloudflare API
type CloudflareAPIInterface interface {
	// ValidateTurnstileToken validates the turnstile token
	ValidateTurnstileToken(ctx context.Context, token string) (ValidateTurnstileTokenResponse, error)
}

// LoginTurnstileAPIInterface adds the trusted client IP used for login verification.
// Registration keeps using CloudflareAPIInterface and its existing behavior.
type LoginTurnstileAPIInterface interface {
	ValidateTurnstileTokenWithRemoteIP(ctx context.Context, token, remoteIP string) (ValidateTurnstileTokenResponse, error)
}
