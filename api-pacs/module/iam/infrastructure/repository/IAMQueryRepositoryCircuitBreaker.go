package repository

import (
	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/iam/domain/repository"
)

// IAMQueryRepositoryCircuitBreaker is the circuit breaker for the iam query repository
type IAMQueryRepositoryCircuitBreaker struct {
	repository.IAMQueryRepositoryInterface
}

// GetTokenSession is a decorator for the get tenant user by id
func (repository *IAMCommandRepositoryCircuitBreaker) GetTokenSession(key string) (entity.TokenSession, error) {
	output := make(chan entity.TokenSession, 1)
	hystrix.ConfigureCommand("select_tenant_user_by_id", config.Settings())
	errors := hystrix.Go("select_tenant_user_by_id", func() error {
		user, err := repository.IAMCommandRepositoryInterface.GetTokenSession(key)
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
		return entity.TokenSession{}, err
	}
}
