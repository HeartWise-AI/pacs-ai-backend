package repository

import (
	"context"

	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

type TenantQueryRepositoryInterface interface {
	SelectTenants(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenant, error)
}
