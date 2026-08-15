package types

import "context"

type SignInWithPasswordRequest struct {
	TenantID string
	Email    string
	Password string
}

type SignInWithPasswordResponse struct {
	IDToken string `json:"idToken"`
	LocalID string `json:"localId"`
	Email   string `json:"email"`
}

type IdentityToolkitAPIInterface interface {
	SignInWithPassword(ctx context.Context, data SignInWithPasswordRequest) (SignInWithPasswordResponse, error)
}
