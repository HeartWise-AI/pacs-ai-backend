package repository

import (
	"context"

	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserQueryRepositoryInterface interface {
	SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error)
	SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error)
}
