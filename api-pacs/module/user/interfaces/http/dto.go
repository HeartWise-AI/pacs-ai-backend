package http

import (
	"unicode"

	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = newValidator()
	ValidationErrors map[string]string   = map[string]string{
		"CreateTenantOwnerRequest.TenantID":           "Tenant ID is required.",
		"CreateTenantUserRequest.Role":                "Role is required.",
		"CreateTenantUserRequest.Name":                "Name is required.",
		"CreateTenantUserRequest.Email":               "Email is required.",
		"CreateTenantUserRequest.LicenseNo":           "License number is required.",
		"CreateTenantUserRequest.Specialty":           "Specialty is required.",
		"DeleteTenantUserRequest.UserID":              "User ID is required.",
		"SendTenantEmailInviteRequest.Email":          "Valid email is required.",
		"RegisterTenantUserRequest.TenantID":          "Tenant ID is required and must not exceed 128 characters.",
		"RegisterTenantUserRequest.TurnstileToken":    "Turnstile token is required and must not exceed 4096 characters.",
		"RegisterTenantUserRequest.Name":              "Name is required and must not exceed 100 characters.",
		"RegisterTenantUserRequest.Email":             "A valid email address is required.",
		"RegisterTenantUserRequest.Password":          "Password must be 8 to 128 characters and contain uppercase, lowercase, and special characters.",
		"RegisterTenantUserRequest.LicenseNo":         "License number is required and must not exceed 100 characters.",
		"RegisterTenantUserRequest.Specialty":         "Specialty is required and must not exceed 100 characters.",
		"RegisterTenantUserRequest.Code":              "Invitation code must not exceed 256 characters.",
		"ResendTenantEmailInviteRequest.ID":           "ID is required.",
		"UpdateTenantUserRequest.ID":                  "ID is required.",
		"UpdateTenantUserRequest.Role":                "Role is required.",
		"UpdateTenantUserRequest.Name":                "Name is required.",
		"UpdateTenantUserRequest.LicenseNo":           "License number is required.",
		"UpdateTenantUserRequest.Specialty":           "Specialty is required.",
		"UpdateTenantUserPasswordRequest.NewPassword": "New password is required.",
	}
)

func newValidator() *validator.Validate {
	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.RegisterValidation("public_password", validatePublicPassword); err != nil {
		panic(err)
	}
	return validate
}

func validatePublicPassword(field validator.FieldLevel) bool {
	password := field.Field().String()
	var hasUpper, hasLower, hasSpecial bool
	for _, character := range password {
		hasUpper = hasUpper || unicode.IsUpper(character)
		hasLower = hasLower || unicode.IsLower(character)
		hasSpecial = hasSpecial || unicode.IsPunct(character) || unicode.IsSymbol(character)
	}
	return hasUpper && hasLower && hasSpecial
}

type CreateTenantOwnerRequest struct {
	TenantID  string `json:"tenantId" validate:"required"`
	Role      string `json:"role" validate:"required"`
	Name      string `json:"name" validate:"required"`
	Email     string `json:"email" validate:"required"`
	LicenseNo string `json:"licenseNo" validate:"required"`
	Specialty string `json:"specialty" validate:"required"`
}

type CreateTenantUserRequest struct {
	Role      string `json:"role" validate:"required"`
	Name      string `json:"name" validate:"required"`
	Email     string `json:"email" validate:"required"`
	LicenseNo string `json:"licenseNo" validate:"required"`
	Specialty string `json:"specialty" validate:"required"`
}

type DeleteTenantUserRequest struct {
	UserID string `json:"userId" validate:"required"`
}

type SendTenantEmailInviteRequest struct {
	Email string `json:"email" validate:"email"`
}

type RegisterTenantUserRequest struct {
	TenantID       string  `json:"tenantId" validate:"required,max=128"`
	TurnstileToken string  `json:"turnstileToken" validate:"required,max=4096"`
	Name           string  `json:"name" validate:"required,max=100"`
	Email          string  `json:"email" validate:"required,email,max=254"`
	Password       string  `json:"password" validate:"required,min=8,max=128,public_password"`
	LicenseNo      string  `json:"licenseNo" validate:"required,max=100"`
	Specialty      string  `json:"specialty" validate:"required,max=100"`
	Code           *string `json:"code" validate:"omitempty,max=256"`
	// Role is accepted only for compatibility with the current frontend and is
	// never passed to the service. Public registration remains server-owned USER.
	Role *string `json:"role" validate:"omitempty,max=64"`
}

type ResendTenantEmailInviteRequest struct {
	ID string `json:"id" validate:"required"`
}

type UpdateTenantUserRequest struct {
	ID        string `json:"id" validate:"required"`
	Role      string `json:"role" validate:"required"`
	Name      string `json:"name" validate:"required"`
	LicenseNo string `json:"licenseNo" validate:"required"`
	Specialty string `json:"specialty" validate:"required"`
}

type UpdateTenantUserPasswordRequest struct {
	NewPassword string `json:"newPassword" validate:"required"`
}

type UpdateUserMetadataRequest struct {
	Metadata map[string]interface{} `json:"metadata"`
}

type CreateTenantUserResponse struct {
	Password string `json:"password" validate:"required"`
}

type GetTenantUserResponse struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenantId"`
	Role              string `json:"role"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	LicenseNo         string `json:"licenseNo"`
	Specialty         string `json:"specialty"`
	IsEmailVerified   bool   `json:"isEmailVerified"`
	IsAccountDisabled bool   `json:"isAccountDisabled"`
	IsConsentSigned   bool   `json:"isConsentSigned"`
	IsAdminCreated    bool   `json:"isAdminCreated"`
	CreatedAt         uint   `json:"createdAt"`
	UpdatedAt         uint   `json:"updatedAt"`
}

type GetTenantUserEmailInviteResponse struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenantId"`
	Code       string  `json:"code"`
	Email      string  `json:"email"`
	ExpiresAt  uint64  `json:"expiresAt"`
	VerifiedAt *uint64 `json:"verifiedAt"`
	CreatedAt  uint64  `json:"createdAt"`
	UpdatedAt  uint64  `json:"updatedAt"`
}

type GetUserMetadataResponse struct {
	UserID    string                 `json:"userId"`
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt uint64                 `json:"createdAt"`
	UpdatedAt uint64                 `json:"updatedAt"`
}
