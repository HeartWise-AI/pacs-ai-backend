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

// GetTokenSession is a decorator for the get token session
func (repository *IAMQueryRepositoryCircuitBreaker) GetTokenSession(key string) (entity.TokenSession, error) {
	output := make(chan entity.TokenSession, 1)
	hystrix.ConfigureCommand("get_token_session", config.Settings())
	errors := hystrix.Go("get_token_session", func() error {
		user, err := repository.IAMQueryRepositoryInterface.GetTokenSession(key)
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
