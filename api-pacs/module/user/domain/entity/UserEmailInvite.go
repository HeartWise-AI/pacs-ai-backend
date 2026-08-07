package entity

// UserEmailInvite holds the email invite entity fields
type UserEmailInvite struct {
	ID         string `firestore:"_id,omitempty"`
	TenantID   string `firestore:"tenant_id"`
	Code       string `firestore:"code"`
	Email      string `firestore:"email"`
	ExpiresAt  int    `firestore:"expires_at"`
	VerifiedAt *int   `firestore:"verified_at"`
	CreatedAt  int    `firestore:"created_at"`
	UpdatedAt  int    `firestore:"updated_at"`
}

// GetModelName returns the model name of user email invite entity that can be used for naming schemas
func (entity *UserEmailInvite) GetModelName() string {
	return "user_email_invites"
}
