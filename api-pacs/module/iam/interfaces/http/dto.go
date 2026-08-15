package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"ForgotTenantUserPasswordRequest.TenantID": "Tenant ID is required.",
		"ForgotTenantUserPasswordRequest.Email":    "Email is required.",
		"LoginTenantUserRequest.TenantID":          "Tenant ID is required.",
		"LoginTenantUserRequest.Email":             "A valid email is required and must not exceed 256 characters.",
		"LoginTenantUserRequest.Password":          "Password is required and must not exceed 4096 characters.",
		"LoginTenantUserRequest.TurnstileToken":    "Turnstile token must not exceed 2048 characters.",
		"VerifyTenantUserEmailRequest.TenantID":    "Tenant ID is required.",
		"VerifyTenantUserEmailRequest.Email":       "Email is required.",
	}
)

type ForgotTenantUserPasswordRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	Email    string `json:"email" validate:"required"`
}

type LoginTenantUserRequest struct {
	TenantID       string `json:"tenantId" validate:"required,max=128"`
	Email          string `json:"email" validate:"required,email,max=256"`
	Password       string `json:"password" validate:"required,max=4096"`
	TurnstileToken string `json:"turnstileToken,omitempty" validate:"omitempty,max=2048"`
}

type VerifyTenantUserEmailRequest struct {
	TenantID string `json:"tenantId" validate:"required"`
	Email    string `json:"email" validate:"required"`
}

type LoginTenantUserResponse struct {
	SessionToken string `json:"sessionToken"`
}
