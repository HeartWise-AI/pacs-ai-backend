package repository

import (
	"context"

	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

type TenantQueryRepositoryInterface interface {
	SelectTenantByID(ctx context.Context, tenantID string) (repositoryTypes.GetTenant, error)
}
