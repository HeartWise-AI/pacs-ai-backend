package application

import (
	"context"
)

// IAMCommandServiceInterface holds the implementable methods for the iam command service
type IAMCommandServiceInterface interface {
	ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error
	LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error)
	VerifyTenantUserEmail(ctx context.Context, tenantID, email string) error
}
