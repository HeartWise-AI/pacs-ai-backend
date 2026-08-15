package entity

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	PolicyTermsOfService = "TERMS_OF_SERVICE"
	PolicyPrivacyPolicy  = "PRIVACY_POLICY"

	PolicyAcceptanceSourceRegistration  = "REGISTRATION"
	PolicyAcceptanceSourceAuthenticated = "AUTHENTICATED"
)

// PolicyReference identifies one immutable published policy version.
type PolicyReference struct {
	PolicyKey string
	Version   string
}

// UserPolicyAcceptance is an append-only audit record. General policy
// acceptance intentionally remains independent from clinical informed consent.
type UserPolicyAcceptance struct {
	TenantID   string `firestore:"tenant_id"`
	UserID     string `firestore:"user_id"`
	PolicyKey  string `firestore:"policy_key"`
	Version    string `firestore:"version"`
	AcceptedAt int64  `firestore:"accepted_at"`
	Source     string `firestore:"source"`
}

// GetModelName returns the Firestore collection name.
func (entity *UserPolicyAcceptance) GetModelName() string {
	return "user_policy_acceptances"
}

// DocumentID creates a stable, non-identifying key so accepting the same
// policy version twice is idempotent while later versions remain append-only.
func (entity UserPolicyAcceptance) DocumentID() string {
	payload := entity.TenantID + "\x00" + entity.UserID + "\x00" + entity.PolicyKey + "\x00" + entity.Version
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// Reference returns the immutable policy identity stored by this acceptance.
func (entity UserPolicyAcceptance) Reference() PolicyReference {
	return PolicyReference{PolicyKey: entity.PolicyKey, Version: entity.Version}
}
