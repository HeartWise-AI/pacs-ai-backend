package application

import (
	"context"

	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryServiceInterface holds the implementable methods for the tenant query service
type TenantQueryServiceInterface interface {
	GetTenants(ctx context.Context, tenantID string) ([]types.GetTenant, error)
}
