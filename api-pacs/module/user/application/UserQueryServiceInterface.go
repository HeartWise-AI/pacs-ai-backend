package application

import (
	"context"

	"api-pacs/module/user/infrastructure/service/types"
)

// UserQueryServiceInterface holds the implementable methods for the user query service
type UserQueryServiceInterface interface {
	GetTenantUserByID(ctx context.Context, tenantID, id string) (types.GetTenantUser, error)
}
