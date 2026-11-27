package repository

import (
	"time"

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
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_token_session", config.Settings())
	errors := hystrix.Go("delete_token_session", func() error {
		err := repository.IAMCommandRepositoryInterface.DeleteTokenSession(key)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// IncrementPublicEmailRequests is the decorator for counting public email requests.
func (repository *IAMCommandRepositoryCircuitBreaker) IncrementPublicEmailRequests(key string, window time.Duration) (int64, error) {
	output := make(chan int64, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("increment_public_email_requests", config.Settings())
	errors := hystrix.Go("increment_public_email_requests", func() error {
		count, err := repository.IAMCommandRepositoryInterface.IncrementPublicEmailRequests(key, window)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- count
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return 0, err
	case err := <-errors:
		return 0, err
	}
}

// IsEmailCooldownActive is the decorator for checking the public email cooldown.
func (repository *IAMCommandRepositoryCircuitBreaker) IsEmailCooldownActive(key string) (bool, error) {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("is_email_cooldown_active", config.Settings())
	errors := hystrix.Go("is_email_cooldown_active", func() error {
		active, err := repository.IAMCommandRepositoryInterface.IsEmailCooldownActive(key)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- active
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return false, err
	case err := <-errors:
		return false, err
	}
}

// SetEmailCooldown is the decorator for setting the public email cooldown.
func (repository *IAMCommandRepositoryCircuitBreaker) SetEmailCooldown(key string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("set_email_cooldown", config.Settings())
	errors := hystrix.Go("set_email_cooldown", func() error {
		err := repository.IAMCommandRepositoryInterface.SetEmailCooldown(key)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}

// SetTokenSession is the decorator for the user repository to set token session
func (repository *IAMCommandRepositoryCircuitBreaker) SetTokenSession(data repositoryTypes.SetTokenSession) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("set_token_session", config.Settings())
	errors := hystrix.Go("set_token_session", func() error {
		err := repository.IAMCommandRepositoryInterface.SetTokenSession(data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- true
		return nil
	}, nil)

	select {
	case <-output:
		return nil
	case err := <-errChan:
		return err
	case err := <-errors:
		return err
	}
}
