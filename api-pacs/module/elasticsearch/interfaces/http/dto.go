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
		"LoginTenantUserRequest.IDToken":           "ID token is required.",
		"VerifyTenantUserEmailRequest.TenantID":    "Tenant ID is required.",
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

type AdminMemberLogResponse struct {
	TenantID   string `json:"sessionId"`
	TenantName string `json:"tenantName"`
	UserID     string `json:"userId"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	LicenseNo  string `json:"licenseNo"`
	Specialty  string `json:"specialty"`
	Action     string `json:"action"`
	Timestamp  uint   `json:"timestamp"`
}

type LoginLogResponse struct {
	SessionID  string `json:"sessionId"`
	TenantID   string `json:"tenantId"`
	TenantName string `json:"tenantName"`
	UserID     string `json:"userId"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Specialty  string `json:"specialty"`
	Timestamp  uint   `json:"timestamp"`
}

type ModalityStudyLogResponse struct {
	TenantID   string `json:"tenantId"`
	TenantName string `json:"tenantName"`
	TenantAET  string `json:"tenantAet"`
	UserID     string `json:"userId"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	QueryID    string `json:"queryId"`
	Timestamp  uint   `json:"timestamp"`
}

type RetrievedStudyLogResponse struct {
	TenantID         string `json:"tenantId"`
	TenantName       string `json:"tenantName"`
	TenantAET        string `json:"tenantAet"`
	UserID           string `json:"userId"`
	Email            string `json:"email"`
	Name             string `json:"name"`
	StudyInstanceUID string `json:"studyInstanceUid"`
	QueryID          string `json:"queryId"`
	AnswerIndex      uint   `json:"answerIndex"`
	Timestamp        uint   `json:"timestamp"`
}

type LoginTenantUserResponse struct {
	SessionToken string `json:"sessionToken"`
}
