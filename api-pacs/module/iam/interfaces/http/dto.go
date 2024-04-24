package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"ForgotTenantUserPasswordRequest.TenantID": "Tenant id is required.",
		"ForgotTenantUserPasswordRequest.Email":    "Email is required.",
		"LoginTenantUserRequest.TenantID":          "Tenant id is required.",
		"LoginTenantUserRequest.IDToken":           "ID token is required.",
		"VerifyTenantUserEmailRequest.TenantID":    "Tenant id is required.",
		"VerifyTenantUserEmailRequest.Email":       "Email is required.",
	}
)

type ForgotTenantUserPasswordRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	Email    string `json:"email" validate:"required"`
}

type LoginTenantUserRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	IDToken  string `json:"idToken" validate:"required"`
}

type VerifyTenantUserEmailRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	Email    string `json:"email" validate:"required"`
}

type LoginTenantUserResponse struct {
	SessionToken string `json:"sessionToken"`
}
