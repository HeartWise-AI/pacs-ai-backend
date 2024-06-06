package application

import (
	"context"

	"api-pacs/module/iam/infrastructure/service/types"
)

// IAMCommandServiceInterface holds the implementable methods for the iam command service
type IAMCommandServiceInterface interface {
	ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error
	LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error)
	SetTokenSession(ctx context.Context, data types.SetTokenSession) error
	VerifyTenantUserEmail(ctx context.Context, tenantID, email string) error
}
