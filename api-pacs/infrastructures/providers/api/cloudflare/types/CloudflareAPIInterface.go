package types

import "context"

// CloudflareAPIInterface is the interface for the Cloudflare API
type CloudflareAPIInterface interface {
	// ValidateTurnstileToken validates the turnstile token
	ValidateTurnstileToken(ctx context.Context, token string) (ValidateTurnstileTokenResponse, error)
}
