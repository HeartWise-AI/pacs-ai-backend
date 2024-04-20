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

// DeleteUser is the decorator for the user repository to delete user
func (repository *UserCommandRepositoryCircuitBreaker) DeleteUser(ctx context.Context, tenantID, id string) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("delete_user", config.Settings())
	errors := hystrix.Go("delete_user", func() error {
		err := repository.UserCommandRepositoryInterface.DeleteUser(ctx, tenantID, id)
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

// InsertUser is the decorator for the user repository to insert user
func (repository *UserCommandRepositoryCircuitBreaker) InsertUser(ctx context.Context, data repositoryTypes.CreateUser) (string, error) {
	output := make(chan string, 1)
	hystrix.ConfigureCommand("insert_b2c_inquiry", config.Settings())
	errors := hystrix.Go("insert_b2c_inquiry", func() error {
		id, err := repository.UserCommandRepositoryInterface.InsertUser(ctx, data)
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

// UpdateUser decorator pattern to update user
func (repository *UserCommandRepositoryCircuitBreaker) UpdateUserPassword(ctx context.Context, data repositoryTypes.UpdateUserPassword) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("update_user_password", config.Settings())
	errors := hystrix.Go("update_user_password", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateUserPassword(ctx, data)
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
