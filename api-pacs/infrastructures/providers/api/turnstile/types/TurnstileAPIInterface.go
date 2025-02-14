package types

import "context"

// TurnstileAPIInterface is the interface for the Turnstile API
type TurnstileAPIInterface interface {
	// ValidateTurnstileToken validates the turnstile token
	ValidateTurnstileToken(ctx context.Context, token string) (ValidateTurnstileTokenResponse, error)
}
