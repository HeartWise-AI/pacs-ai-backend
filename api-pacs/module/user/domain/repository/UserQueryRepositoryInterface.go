package repository

import (
	"context"

	"api-pacs/module/user/domain/entity"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserQueryRepositoryInterface interface {
	SelectTenantUserByEmail(ctx context.Context, tenantID, email string) error
	SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error)
	SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error)
	SelectTenantUserEmailInviteByEmail(ctx context.Context, tenantID, email string) (entity.UserEmailInvite, error)
	SelectTenantUserEmailInviteByID(ctx context.Context, tenantID, ID string) (entity.UserEmailInvite, error)
	SelectUserMetadataByID(ctx context.Context, userID string) (entity.UserMetadata, error)
}
