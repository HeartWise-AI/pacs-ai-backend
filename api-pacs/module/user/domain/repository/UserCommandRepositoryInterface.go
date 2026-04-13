package repository

import (
	"context"

	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserCommandRepositoryInterface interface {
	DeleteTenantUser(ctx context.Context, tenantID, id string) error
	InsertTenantUser(ctx context.Context, data repositoryTypes.CreateTenantUser) (string, error)
	InsertTenantUserEmailInvite(ctx context.Context, data repositoryTypes.CreateTenantUserEmailInvite) error
	UpdateTenantUser(ctx context.Context, data repositoryTypes.UpdateTenantUser) error
	UpdateTenantUserPassword(ctx context.Context, data repositoryTypes.UpdateTenantUserPassword) error
	UpdateTenantUserConsent(ctx context.Context, data repositoryTypes.UpdateTenantUserConsent) error
	UpdateTenantUserEmailInvite(ctx context.Context, data repositoryTypes.UpdateTenantUserEmailInvite) error
	UpdateTenantUserEmailInviteVerifiedAt(ctx context.Context, ID string) error
	UpsertUserMetadata(ctx context.Context, data repositoryTypes.UpsertUserMetadata) error
}
