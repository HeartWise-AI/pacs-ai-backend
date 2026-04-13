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
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("delete_tenant_user", config.Settings())
	errors := hystrix.Go("delete_tenant_user", func() error {
		err := repository.UserCommandRepositoryInterface.DeleteTenantUser(ctx, tenantID, id)
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

// InsertTenantUser is the decorator for the user repository to insert tenant user
func (repository *UserCommandRepositoryCircuitBreaker) InsertTenantUser(ctx context.Context, data repositoryTypes.CreateTenantUser) (string, error) {
	output := make(chan string, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_tenant_user", config.Settings())
	errors := hystrix.Go("insert_tenant_user", func() error {
		id, err := repository.UserCommandRepositoryInterface.InsertTenantUser(ctx, data)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- id
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return "", err
	case err := <-errors:
		return "", err
	}
}

// InsertTenantUserEmailInvite is the decorator for the user repository to insert tenant user email invite
func (repository *UserCommandRepositoryCircuitBreaker) InsertTenantUserEmailInvite(ctx context.Context, data repositoryTypes.CreateTenantUserEmailInvite) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("insert_tenant_user_email_invite", config.Settings())
	errors := hystrix.Go("insert_tenant_user_email_invite", func() error {
		err := repository.UserCommandRepositoryInterface.InsertTenantUserEmailInvite(ctx, data)
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

// UpdateTenantUser decorator pattern to update tenant user
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUser(ctx context.Context, data repositoryTypes.UpdateTenantUser) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_tenant_user", config.Settings())
	errors := hystrix.Go("update_tenant_user", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUser(ctx, data)
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

// UpdateTenantUserConsent decorator pattern to update tenant user consent
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUserConsent(ctx context.Context, data repositoryTypes.UpdateTenantUserConsent) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_tenant_user_consent", config.Settings())
	errors := hystrix.Go("update_tenant_user_consent", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUserConsent(ctx, data)
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

// UpdateTenantUserEmailInvite decorator pattern to update tenant user email invite
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUserEmailInvite(ctx context.Context, data repositoryTypes.UpdateTenantUserEmailInvite) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_tenant_user_email_invite", config.Settings())
	errors := hystrix.Go("update_tenant_user_email_invite", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUserEmailInvite(ctx, data)
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

// UpdateTenantUserEmailInviteVerifiedAt decorator pattern to update tenant user email invite verified at
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUserEmailInviteVerifiedAt(ctx context.Context, ID string) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_tenant_user_email_invite_verified_at", config.Settings())
	errors := hystrix.Go("update_tenant_user_email_invite_verified_at", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUserEmailInviteVerifiedAt(ctx, ID)
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

// UpdateTenantUserPassword decorator pattern to update tenant user password
func (repository *UserCommandRepositoryCircuitBreaker) UpdateTenantUserPassword(ctx context.Context, data repositoryTypes.UpdateTenantUserPassword) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("update_tenant_user_password", config.Settings())
	errors := hystrix.Go("update_tenant_user_password", func() error {
		err := repository.UserCommandRepositoryInterface.UpdateTenantUserPassword(ctx, data)
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

// UpsertUserMetadata decorator pattern to upsert user metadata
func (repository *UserCommandRepositoryCircuitBreaker) UpsertUserMetadata(ctx context.Context, data repositoryTypes.UpsertUserMetadata) error {
	output := make(chan bool, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("upsert_user_metadata", config.Settings())
	errors := hystrix.Go("upsert_user_metadata", func() error {
		err := repository.UserCommandRepositoryInterface.UpsertUserMetadata(ctx, data)
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
