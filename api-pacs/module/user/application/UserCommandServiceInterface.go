package application

import (
	"api-pacs/module/user/infrastructure/service/types"
	"context"
)

// UserCommandServiceInterface holds the implementable methods for the user command service
type UserCommandServiceInterface interface {
	AddTenantUser(ctx context.Context, data types.AddTenantUser) (string, error)
	DeleteTenantUser(ctx context.Context, tenantID, uid string) error
	UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error
}
