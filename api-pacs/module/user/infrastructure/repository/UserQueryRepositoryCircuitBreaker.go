package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

// UserQueryRepositoryCircuitBreaker is the circuit breaker for the user query repository
type UserQueryRepositoryCircuitBreaker struct {
	repository.UserQueryRepositoryInterface
}

// SelectTenantUsers is a decorator for the select b2c users repository
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error) {
	output := make(chan []repositoryTypes.GetTenantUser, 1)
	hystrix.ConfigureCommand("select_tenant_users", config.Settings())
	errors := hystrix.Go("select_tenant_users", func() error {
		points, err := repository.UserQueryRepositoryInterface.SelectTenantUsers(ctx, tenantID)
		if err != nil {
			return err
		}

		output <- points
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return []repositoryTypes.GetTenantUser{}, err
	}
}

// SelectTenantUserByID is a decorator for the get tenant user by id
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error) {
	output := make(chan repositoryTypes.GetTenantUser, 1)
	hystrix.ConfigureCommand("select_tenant_user_by_id", config.Settings())
	errors := hystrix.Go("select_tenant_user_by_id", func() error {
		user, err := repository.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, tenantID, id)
		if err != nil {
			return err
		}

		output <- user
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return repositoryTypes.GetTenantUser{}, err
	}
}
