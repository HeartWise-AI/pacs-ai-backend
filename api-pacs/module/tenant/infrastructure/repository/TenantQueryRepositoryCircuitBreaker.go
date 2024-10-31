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

// SelectTenantByID is a decorator for the get tenant by id
func (repository *TenantQueryRepositoryCircuitBreaker) SelectTenantByID(ctx context.Context, tenantID string) (repositoryTypes.GetTenant, error) {
	output := make(chan repositoryTypes.GetTenant, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_by_id", config.Settings())
	errors := hystrix.Go("select_tenant_by_id", func() error {
		tenant, err := repository.TenantQueryRepositoryInterface.SelectTenantByID(ctx, tenantID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- tenant
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return repositoryTypes.GetTenant{}, err
	case err := <-errors:
		return repositoryTypes.GetTenant{}, err
	}
}
