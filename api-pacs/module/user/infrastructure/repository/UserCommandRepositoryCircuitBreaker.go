package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

// UserCommandRepositoryCircuitBreaker circuit breaker for user command repository
type UserCommandRepositoryCircuitBreaker struct {
	repository.UserCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// DeleteTenantUser is the decorator for the user repository to delete tenant user
func (repository *UserCommandRepositoryCircuitBreaker) DeleteTenantUser(ctx context.Context, tenantID, id string) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("delete_tenant_user", config.Settings())
	errors := hystrix.Go("delete_tenant_user", func() error {
		err := repository.UserCommandRepositoryInterface.DeleteTenantUser(ctx, tenantID, id)
		if err != nil {
			return err
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errors:
		return err
	}
}

// InsertTenantUser is the decorator for the user repository to insert tenant user
func (repository *UserCommandRepositoryCircuitBreaker) InsertTenantUser(ctx context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	output := make(chan string, 1)
	hystrix.ConfigureCommand("insert_tenant_user", config.Settings())
	errors := hystrix.Go("insert_tenant_user", func() error {
		id, err := repository.UserCommandRepositoryInterface.InsertTenantUser(ctx, data)
		if err != nil {
			return err
		}

		output <- id
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errors:
		return "", err
	}
}

// UpdateTenantUser decorator pattern to update tenant user
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUser(ctx context.Context, data repositoryTypes.UpdateTenantUser) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("update_tenant_user", config.Settings())
	errors := hystrix.Go("update_tenant_user", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUser(ctx, data)
		if err != nil {
			return err
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errors:
		return err
	}
}

// UpdateTenantUserPassword decorator pattern to update tenant user password
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUserPassword(ctx context.Context, data repositoryTypes.UpdateTenantUserPassword) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("update_tenant_user_password", config.Settings())
	errors := hystrix.Go("update_tenant_user_password", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUserPassword(ctx, data)
		if err != nil {
			return err
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errors:
		return err
	}
}
