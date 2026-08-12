package application

import (
	"context"

	"api-pacs/module/iam/infrastructure/service/types"
)

// IAMCommandServiceInterface holds the implementable methods for the iam command service
type IAMCommandServiceInterface interface {
	ClearUserSuspension(ctx context.Context, tenantID, userID string) error
	ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error
	LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error)
	RevokeUserSessions(ctx context.Context, tenantID, userID string) error
	SetTokenSession(ctx context.Context, data types.SetTokenSession) error
	SetUserSuspended(ctx context.Context, tenantID, userID string) error
	VerifyTenantUserEmail(ctx context.Context, tenantID, email string) error
}
