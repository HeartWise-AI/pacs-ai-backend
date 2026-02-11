package repository

import (
	"context"

	"api-pacs/module/user/domain/entity"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserQueryRepositoryInterface interface {
	SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error)
	SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error)
	SelectUserMetadataByUserID(ctx context.Context, userID string) (entity.UserMetadata, error)
}
