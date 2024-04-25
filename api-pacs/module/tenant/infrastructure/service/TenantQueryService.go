package service

import (
	"context"

	"api-pacs/module/tenant/domain/repository"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantQueryService handles the Tenant query service logic
type TenantQueryService struct {
	repository.TenantQueryRepositoryInterface
}

// GetTenants get tenants by id
func (service *TenantQueryService) GetTenants(ctx context.Context, tenantID string) ([]types.GetTenant, error) {
	res, err := service.TenantQueryRepositoryInterface.SelectTenants(ctx, tenantID)
	if err != nil {
		return []types.GetTenant{}, err
	}

	var tenants []types.GetTenant

	for _, tenant := range res {
		tenants = append(tenants, types.GetTenant{
			ID:        tenant.ID,
			Name:      tenant.Name,
			Address:   tenant.Address,
			CreatedAt: tenant.CreatedAt,
			UpdatedAt: tenant.UpdatedAt,
		})
	}

	return tenants, nil
}
