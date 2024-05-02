package http

import (
	"github.com/go-playground/validator/v10"
)

var (
	Validate         *validator.Validate = validator.New(validator.WithRequiredStructEnabled())
	ValidationErrors map[string]string   = map[string]string{
		"CreateTenantOwnerRequest.TenantID":           "Tenant ID is required.",
		"CreateTenantUserRequest.Role":                "Role is required.",
		"CreateTenantUserRequest.Name":                "Name is required.",
		"CreateTenantUserRequest.Email":               "Email is required.",
		"CreateTenantUserRequest.LicenseNo":           "License number is required.",
		"CreateTenantUserRequest.Specialty":           "Specialty is required.",
		"DeleteTenantUserRequest.UserID":              "User ID is required.",
		"UpdateTenantUserRequest.ID":                  "ID is required.",
		"UpdateTenantUserRequest.Role":                "Role is required.",
		"UpdateTenantUserRequest.Name":                "Name is required.",
		"UpdateTenantUserRequest.LicenseNo":           "License number is required.",
		"UpdateTenantUserRequest.Specialty":           "Specialty is required.",
		"UpdateTenantUserPasswordRequest.NewPassword": "New password is required.",
	}
)

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
	CreatedAt         uint   `json:"createdAt"`
	UpdatedAt         uint   `json:"updatedAt"`
}
