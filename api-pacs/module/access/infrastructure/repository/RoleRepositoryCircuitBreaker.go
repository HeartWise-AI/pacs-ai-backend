package repository

import (
	"context"

	"github.com/afex/hystrix-go/hystrix"

	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/access/domain/entity"
	domainRepository "api-pacs/module/access/domain/repository"
)

var config = hystrix_config.Config{}

// RoleRepositoryCircuitBreaker decorates the role repository with hystrix.
type RoleRepositoryCircuitBreaker struct {
	domainRepository.RoleRepositoryInterface
}

func guarded[T any](name string, call func() (T, error)) (T, error) {
	output := make(chan T, 1)
	errChan := make(chan error, 1)
	hystrix.ConfigureCommand(name, config.Settings())
	errs := hystrix.Go(name, func() error {
		value, err := call()
		if err != nil {
			errChan <- err
			return nil
		}
		output <- value
		return nil
	}, nil)
	var zero T
	select {
	case value := <-output:
		return value, nil
	case err := <-errChan:
		return zero, err
	case err := <-errs:
		return zero, err
	}
}

// ListRoles is a decorator for ListRoles.
func (repository *RoleRepositoryCircuitBreaker) ListRoles(ctx context.Context, tenantID string) ([]entity.CustomRole, error) {
	return guarded("access_list_roles", func() ([]entity.CustomRole, error) {
		return repository.RoleRepositoryInterface.ListRoles(ctx, tenantID)
	})
}

// GetRole is a decorator for GetRole.
func (repository *RoleRepositoryCircuitBreaker) GetRole(ctx context.Context, tenantID, roleID string) (entity.CustomRole, error) {
	return guarded("access_get_role", func() (entity.CustomRole, error) {
		return repository.RoleRepositoryInterface.GetRole(ctx, tenantID, roleID)
	})
}

// SaveRole is a decorator for SaveRole.
func (repository *RoleRepositoryCircuitBreaker) SaveRole(ctx context.Context, role entity.CustomRole) error {
	_, err := guarded("access_save_role", func() (struct{}, error) {
		return struct{}{}, repository.RoleRepositoryInterface.SaveRole(ctx, role)
	})
	return err
}

// DeleteRole is a decorator for DeleteRole.
func (repository *RoleRepositoryCircuitBreaker) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	_, err := guarded("access_delete_role", func() (struct{}, error) {
		return struct{}{}, repository.RoleRepositoryInterface.DeleteRole(ctx, tenantID, roleID)
	})
	return err
}

// GetUserAssignment is a decorator for GetUserAssignment.
func (repository *RoleRepositoryCircuitBreaker) GetUserAssignment(ctx context.Context, tenantID, userID string) (domainRepository.UserRoleAssignment, error) {
	return guarded("access_get_user_assignment", func() (domainRepository.UserRoleAssignment, error) {
		return repository.RoleRepositoryInterface.GetUserAssignment(ctx, tenantID, userID)
	})
}

// ListUserAssignments is a decorator for ListUserAssignments.
func (repository *RoleRepositoryCircuitBreaker) ListUserAssignments(ctx context.Context, tenantID string) ([]domainRepository.UserRoleAssignment, error) {
	return guarded("access_list_user_assignments", func() ([]domainRepository.UserRoleAssignment, error) {
		return repository.RoleRepositoryInterface.ListUserAssignments(ctx, tenantID)
	})
}

// SetUserCustomRole is a decorator for SetUserCustomRole.
func (repository *RoleRepositoryCircuitBreaker) SetUserCustomRole(ctx context.Context, tenantID, userID, roleID string) error {
	_, err := guarded("access_set_user_custom_role", func() (struct{}, error) {
		return struct{}{}, repository.RoleRepositoryInterface.SetUserCustomRole(ctx, tenantID, userID, roleID)
	})
	return err
}
