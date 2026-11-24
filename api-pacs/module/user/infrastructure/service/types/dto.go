package types

type CreateTenantUser struct {
	TenantID  string
	Role      string
	Name      string
	Email     string
	LicenseNo string
	Specialty string
}

type GetTenantUser struct {
	ID                string
	TenantID          string
	Role              string
	CustomRoleID      string
	AccessState       string
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

type ChangeTenantUserAccess struct {
	TenantID     string
	ActorUserID  string
	ActorRole    string
	TargetUserID string
	AccessState  string
	Reason       string
	// ActorCanManageMembers is true when the session holds members.manage;
	// it lets a USER account with that custom-role permission act on USER
	// accounts only.
	ActorCanManageMembers bool
}

type DeleteTenantUser struct {
	TenantID     string
	ActorUserID  string
	ActorRole    string
	TargetUserID string
	Reason       string
	// ActorCanManageMembers: see ChangeTenantUserAccess.
	ActorCanManageMembers bool
}

type RegisterTenantUser struct {
	TenantID          string
	TurnstileToken    string
	ClientIP          string
	Name              string
	Email             string
	Password          string
	LicenseNo         string
	Specialty         string
	Code              *string
	PolicyAcceptances []PolicyAcceptanceInput
}

type RegistrationRateLimit struct {
	TenantID string
	Email    string
	ClientIP string
}

type PolicyDefinition struct {
	PolicyKey        string
	Version          string
	Title            string
	URL              string
	EffectiveAt      string
	AcceptanceAction string
	Required         bool
}

type PolicyAcceptanceInput struct {
	PolicyKey string
	Version   string
}

type PolicyStatusItem struct {
	PolicyDefinition
	Accepted   bool
	AcceptedAt *int64
}

type PolicyStatus struct {
	Policies           []PolicyStatusItem
	AcceptanceRequired bool
	EnforcementActive  bool
}

type AcceptPolicies struct {
	TenantID    string
	UserID      string
	Source      string
	Acceptances []PolicyAcceptanceInput
}

type ResetTutorial struct {
	TenantID string
	UserID   string
}

type ResendTenantUserEmailInvite struct {
	ID       string
	TenantID string
}

type SendTenantUserEmailInvite struct {
	TenantID string
	Email    string
}

type UpdateTenantUser struct {
	ID        string
	TenantID  string
	Role      string
	Name      string
	LicenseNo string
	Specialty string
	// ActorUserID and ActorRole identify the session. A USER actor (custom
	// role with members.manage) may only edit other USER accounts and
	// cannot change their role.
	ActorUserID string
	ActorRole   string
}

type UpdateTenantUserPassword struct {
	TenantID    string
	ID          string
	NewPassword string
}

type UpdateUserMetadata struct {
	UserID   string
	Metadata map[string]interface{}
}

// UserViewerTourState is the authenticated user's viewer onboarding tour state.
type UserViewerTourState struct {
	UserID                string
	ViewerTourCompletedAt *int64
}
