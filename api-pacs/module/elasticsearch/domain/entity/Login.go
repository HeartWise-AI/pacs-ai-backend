package entity

import (
	"crypto/sha256"
	"encoding/hex"
)

// Login holds the login entity fields
type Login struct {
	SessionID  string `json:"session_id"`
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Specialty  string `json:"specialty"`
	Timestamp  uint   `json:"timestamp"`
}

// GetModelName returns the model name of login entity that can be used for naming schemas
func (entity *Login) GetModelName() string {
	return "logins"
}

// SessionReference returns a SHA-256 digest that correlates login records
// with a session without being usable as a bearer token. A value that is
// already a digest is returned unchanged, so it is safe to apply on read.
func SessionReference(sessionID string) string {
	if isSessionReference(sessionID) {
		return sessionID
	}
	return HashSessionID(sessionID)
}

// HashSessionID always hashes. Use it when writing a new login record, where the
// input is known to be a live session token whatever its format.
func HashSessionID(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}

func isSessionReference(value string) bool {
	if len(value) != hex.EncodedLen(sha256.Size) {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
