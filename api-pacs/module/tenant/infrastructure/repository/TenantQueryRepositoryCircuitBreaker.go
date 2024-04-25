package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/tenant/domain/repository"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantQueryRepositoryCircuitBreaker is the circuit breaker for the tenant query repository
type TenantQueryRepositoryCircuitBreaker struct {
	repository.TenantQueryRepositoryInterface
}

// SelectTenants is a decorator for the select tenants
func (repository *TenantQueryRepositoryCircuitBreaker) SelectTenants(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenant, error) {
	output := make(chan []repositoryTypes.GetTenant, 1)
	hystrix.ConfigureCommand("select_tenants", config.Settings())
	errors := hystrix.Go("select_tenants", func() error {
		tenants, err := repository.TenantQueryRepositoryInterface.SelectTenants(ctx, tenantID)
		if err != nil {
			return err
		}

		output <- tenants
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return []repositoryTypes.GetTenant{}, err
	}
}
