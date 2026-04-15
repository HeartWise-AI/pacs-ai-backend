package types

import "time"

type CreateTenantUser struct {
	TenantID  string
	Role      string
	Email     string
	Password  string
	Name      string
	LicenseNo string
	Specialty string
}

type CreateTenantUserEmailInvite struct {
	ID        string
	TenantID  string
	Code      string
	Email     string
	ExpiresAt time.Time
}

type GetTenantUser struct {
	ID                string
	TenantID          string
	Role              string
	Name              string
	Email             string
	LicenseNo         string
	Specialty         string
	IsEmailVerified   bool
	IsAccountDisabled bool
	IsConsentSigned   bool
	IsAdminCreated    bool
	CreatedAt         uint
	UpdatedAt         uint
}

type UpdateTenantUser struct {
	ID        string
	TenantID  string
	Role      string
	Name      string
	LicenseNo string
	Specialty string
	UpdatedAt uint
}

type UpdateTenantUserEmailInvite struct {
	ID        string
	Code      string
	ExpiresAt time.Time
}

type UpdateTenantUserEmailInviteVerifiedAt struct {
	ID         string
	VerifiedAt time.Time
}

type UpdateTenantUserConsent struct {
	ID              string
	IsConsentSigned bool
}

type UpdateTenantUserPassword struct {
	ID          string
	TenantID    string
	NewPassword string
}

type UpsertUserMetadata struct {
	UserID   string
	Metadata string
}
