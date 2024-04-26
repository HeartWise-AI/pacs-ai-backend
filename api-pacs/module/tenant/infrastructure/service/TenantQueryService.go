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

// GetTenantByID get tenant by id
func (service *TenantQueryService) GetTenantByID(ctx context.Context, tenantID string) (types.GetTenant, error) {
	tenant, err := service.TenantQueryRepositoryInterface.SelectTenantByID(ctx, tenantID)
	if err != nil {
		return types.GetTenant{}, err
	}

	return types.GetTenant{
		ID:        tenant.ID,
		Name:      tenant.Name,
		Address:   tenant.Address,
		CreatedAt: uint(tenant.CreatedAt),
		UpdatedAt: uint(tenant.UpdatedAt),
	}, nil
}
