package repository

import (
	"context"

	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserCommandRepositoryInterface interface {
	DeleteTenantUser(ctx context.Context, tenantID, id string) error
	InsertTenantUser(ctx context.Context, data repositoryTypes.CreateTenantUser) (string, error)
	UpdateTenantUserPassword(ctx context.Context, data repositoryTypes.UpdateTenantUserPassword) error
}
