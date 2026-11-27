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
	IncrementPublicEmailRequests(key string, window time.Duration) (int64, error)
	IsEmailCooldownActive(key string) (bool, error)
	SetEmailCooldown(key string) error
	SetTokenSession(data repositoryTypes.SetTokenSession) error
	SetUserSuspended(tenantID, userID string) (bool, error)
}
