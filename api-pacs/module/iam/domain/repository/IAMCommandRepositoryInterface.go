package repository

import (
	"time"

	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

type IAMCommandRepositoryInterface interface {
	AcquireUserAccessTransition(tenantID, userID, ownerToken string, ttl time.Duration) (bool, error)
	ClearUserSuspension(tenantID, userID string) error
	DeleteTokenSession(key string) error
	RevokeUserSessions(tenantID, userID string) error
	ReleaseUserAccessTransition(tenantID, userID, ownerToken string) error
	IsEmailVerificationCooldownActive(key string) (bool, error)
	SetEmailVerificationCooldown(key string) error
	SetTokenSession(data repositoryTypes.SetTokenSession) error
	SetUserSuspended(tenantID, userID string) (bool, error)
}
