package application

import (
	"context"

	"api-pacs/module/user/infrastructure/service/types"
)

// UserQueryServiceInterface holds the implementable methods for the user query service
type UserQueryServiceInterface interface {
	GetTenantUsers(ctx context.Context, tenantID string) ([]types.GetTenantUser, error)
	GetTenantUserByID(ctx context.Context, tenantID, id string) (types.GetTenantUser, error)
}
