package repository

import (
	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/iam/domain/repository"
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

// IAMCommandRepositoryCircuitBreaker circuit breaker for iam command repository
type IAMCommandRepositoryCircuitBreaker struct {
	repository.IAMCommandRepositoryInterface
}

var config = hystrix_config.Config{}

// DeleteTokenSession is the decorator for the user repository to delete token session
func (repository *IAMCommandRepositoryCircuitBreaker) DeleteTokenSession(key string) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("delete_token_session", config.Settings())
	errors := hystrix.Go("delete_token_session", func() error {
		err := repository.IAMCommandRepositoryInterface.DeleteTokenSession(key)
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

// SetTokenSession is the decorator for the user repository to set token session
func (repository *IAMCommandRepositoryCircuitBreaker) SetTokenSession(data repositoryTypes.SetTokenSession) error {
	output := make(chan bool, 1)
	hystrix.ConfigureCommand("set_token_session", config.Settings())
	errors := hystrix.Go("set_token_session", func() error {
		err := repository.IAMCommandRepositoryInterface.SetTokenSession(data)
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
