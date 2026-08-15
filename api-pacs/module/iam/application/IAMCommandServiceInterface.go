package application

import (
	"context"
	"time"

	"api-pacs/module/iam/infrastructure/service/types"
)

// IAMCommandServiceInterface holds the implementable methods for the iam command service
type IAMCommandServiceInterface interface {
	AcquireUserAccessTransition(ctx context.Context, tenantID, userID, ownerToken string, ttl time.Duration) (bool, error)
	ClearUserSuspension(ctx context.Context, tenantID, userID string) error
	ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error
	LoginTenantUser(ctx context.Context, data types.LoginTenantUser) (string, error)
	RevokeUserSessions(ctx context.Context, tenantID, userID string) error
	ReleaseUserAccessTransition(ctx context.Context, tenantID, userID, ownerToken string) error
	SetTokenSession(ctx context.Context, data types.SetTokenSession) error
	SetUserSuspended(ctx context.Context, tenantID, userID string) (bool, error)
	VerifyTenantUserEmail(ctx context.Context, tenantID, email string) error
}
