package repository

import (
	"context"

	"api-pacs/module/user/domain/entity"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserQueryRepositoryInterface interface {
	// SelectTenantUserByEmail selects a tenant user by email
	SelectTenantUserByEmail(ctx context.Context, tenantID, email string) (repositoryTypes.GetTenantUser, error)
	// SelectTenantUserByID selects a tenant user by id
	SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error)
	// SelectTenantUsers selects tenant users
	SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error)
	// SelectTenantUserEmailInviteByEmail selects a tenant user email invite by email
	SelectTenantUserEmailInviteByEmail(ctx context.Context, tenantID, email string) (entity.UserEmailInvite, error)
	// SelectTenantUserEmailInviteByID selects a tenant user email invite by id
	SelectTenantUserEmailInviteByID(ctx context.Context, tenantID, ID string) (entity.UserEmailInvite, error)
	// SelectUserMetadataByID selects user metadata by id
	SelectUserMetadataByID(ctx context.Context, userID string) (entity.UserMetadata, error)
}
