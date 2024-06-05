package application

import (
	"context"

	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

// IAMCommandServiceInterface holds the implementable methods for the iam command service
type IAMCommandServiceInterface interface {
	ForgotTenantUserPassword(ctx context.Context, tenantID, email string) error
	LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error)
	SetTokenSession(ctx context.Context, data repositoryTypes.SetTokenSession) error
	VerifyTenantUserEmail(ctx context.Context, tenantID, email string) error
}
