package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/domain/repository"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

// UserQueryRepositoryCircuitBreaker is the circuit breaker for the user query repository
type UserQueryRepositoryCircuitBreaker struct {
	repository.UserQueryRepositoryInterface
}

// SelectTenantUserByEmail is a decorator for the get tenant user by email
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUserByEmail(ctx context.Context, tenantID, email string) (repositoryTypes.GetTenantUser, error) {
	output := make(chan repositoryTypes.GetTenantUser, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_user_by_email", config.Settings())
	errors := hystrix.Go("select_tenant_user_by_email", func() error {
		user, err := repository.UserQueryRepositoryInterface.SelectTenantUserByEmail(ctx, tenantID, email)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- user
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return repositoryTypes.GetTenantUser{}, err
	case err := <-errors:
		return repositoryTypes.GetTenantUser{}, err
	}
}

// SelectTenantUserByID is a decorator for the get tenant user by id
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error) {
	output := make(chan repositoryTypes.GetTenantUser, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_user_by_id", config.Settings())
	errors := hystrix.Go("select_tenant_user_by_id", func() error {
		user, err := repository.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, tenantID, id)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- user
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return repositoryTypes.GetTenantUser{}, err
	case err := <-errors:
		return repositoryTypes.GetTenantUser{}, err
	}
}

// SelectTenantUsers is a decorator for the select tenant users
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error) {
	output := make(chan []repositoryTypes.GetTenantUser, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_users", config.Settings())
	errors := hystrix.Go("select_tenant_users", func() error {
		points, err := repository.UserQueryRepositoryInterface.SelectTenantUsers(ctx, tenantID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- points
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return []repositoryTypes.GetTenantUser{}, err
	case err := <-errors:
		return []repositoryTypes.GetTenantUser{}, err
	}
}

// SelectUserPolicyAcceptances decorates exact-version policy acceptance reads.
func (repository *UserQueryRepositoryCircuitBreaker) SelectUserPolicyAcceptances(ctx context.Context, tenantID, userID string, policies []entity.PolicyReference) ([]entity.UserPolicyAcceptance, error) {
	output := make(chan []entity.UserPolicyAcceptance, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_user_policy_acceptances", config.Settings())
	errors := hystrix.Go("select_user_policy_acceptances", func() error {
		acceptances, err := repository.UserQueryRepositoryInterface.SelectUserPolicyAcceptances(ctx, tenantID, userID, policies)
		if err != nil {
			errChan <- err
			return nil
		}
		output <- acceptances
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return nil, err
	case err := <-errors:
		return nil, err
	}
}

// SelectTenantUserEmailInviteByEmail is a decorator for the get tenant user email invite by email
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUserEmailInviteByEmail(ctx context.Context, tenantID, email string) (entity.UserEmailInvite, error) {
	output := make(chan entity.UserEmailInvite, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_user_email_invite_by_email", config.Settings())
	errors := hystrix.Go("select_tenant_user_email_invite_by_email", func() error {
		userEmailInvite, err := repository.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByEmail(ctx, tenantID, email)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- userEmailInvite
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return entity.UserEmailInvite{}, err
	case err := <-errors:
		return entity.UserEmailInvite{}, err
	}
}

// SelectTenantUserEmailInviteByID is a decorator for the get tenant user email invite by id
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUserEmailInviteByID(ctx context.Context, tenantID, ID string) (entity.UserEmailInvite, error) {
	output := make(chan entity.UserEmailInvite, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_user_email_invite_by_id", config.Settings())
	errors := hystrix.Go("select_tenant_user_email_invite_by_id", func() error {
		userEmailInvite, err := repository.UserQueryRepositoryInterface.SelectTenantUserEmailInviteByID(ctx, tenantID, ID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- userEmailInvite
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return entity.UserEmailInvite{}, err
	case err := <-errors:
		return entity.UserEmailInvite{}, err
	}
}

// SelectTenantUserEmailInvites is a decorator for the select tenant user email invites
func (repository *UserQueryRepositoryCircuitBreaker) SelectTenantUserEmailInvites(ctx context.Context, tenantID string) ([]entity.UserEmailInvite, error) {
	output := make(chan []entity.UserEmailInvite, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_tenant_user_email_invites", config.Settings())
	errors := hystrix.Go("select_tenant_user_email_invites", func() error {
		userEmailInvites, err := repository.UserQueryRepositoryInterface.SelectTenantUserEmailInvites(ctx, tenantID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- userEmailInvites
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return []entity.UserEmailInvite{}, err
	case err := <-errors:
		return []entity.UserEmailInvite{}, err
	}
}

// SelectUserMetadataByID is a decorator for the select user metadata by id
func (repository *UserQueryRepositoryCircuitBreaker) SelectUserMetadataByID(ctx context.Context, userID string) (entity.UserMetadata, error) {
	output := make(chan entity.UserMetadata, 1)
	errChan := make(chan error, 1)

	hystrix.ConfigureCommand("select_user_metadata_by_id", config.Settings())
	errors := hystrix.Go("select_user_metadata_by_id", func() error {
		userMetadata, err := repository.UserQueryRepositoryInterface.SelectUserMetadataByID(ctx, userID)
		if err != nil {
			errChan <- err
			return nil
		}

		output <- userMetadata
		return nil
	}, nil)

	select {
	case out := <-output:
		return out, nil
	case err := <-errChan:
		return entity.UserMetadata{}, err
	case err := <-errors:
		return entity.UserMetadata{}, err
	}
}
