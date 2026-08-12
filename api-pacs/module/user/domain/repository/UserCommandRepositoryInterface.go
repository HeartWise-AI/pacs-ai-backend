package repository

import (
	"context"

	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserCommandRepositoryInterface interface {
	// DeleteTenantUser deletes a tenant user
	DeleteTenantUser(ctx context.Context, tenantID, id string) error
	// DeleteTenantUserEmailInvite deletes a tenant user email invite
	DeleteTenantUserEmailInvite(ctx context.Context, ID string) error
	// InsertTenantUser inserts a tenant user and returns the inserted ID
	InsertTenantUser(ctx context.Context, data repositoryTypes.CreateTenantUser) (string, error)
	// GenerateTenantUserEmailVerificationLink generates a Firebase email verification link
	GenerateTenantUserEmailVerificationLink(ctx context.Context, tenantID, email string) (string, error)
	// InsertTenantUserEmailInvite inserts a tenant user email invite
	InsertTenantUserEmailInvite(ctx context.Context, data repositoryTypes.CreateTenantUserEmailInvite) error
	// UpdateTenantUser updates a tenant user
	UpdateTenantUser(ctx context.Context, data repositoryTypes.UpdateTenantUser) error
	UpdateTenantUserAccessState(ctx context.Context, data repositoryTypes.UpdateTenantUserAccessState) error
	// UpdateTenantUserPassword updates a tenant user password
	UpdateTenantUserPassword(ctx context.Context, data repositoryTypes.UpdateTenantUserPassword) error
	// UpdateTenantUserConsent updates a tenant user consent
	UpdateTenantUserConsent(ctx context.Context, data repositoryTypes.UpdateTenantUserConsent) error
	// UpdateTenantUserEmailInvite updates a tenant user email invite
	UpdateTenantUserEmailInvite(ctx context.Context, data repositoryTypes.UpdateTenantUserEmailInvite) error
	// UpdateTenantUserEmailInviteVerifiedAt updates the user email invite verified at
	UpdateTenantUserEmailInviteVerifiedAt(ctx context.Context, ID string) error
	// UpsertUserMetadata upserts user metadata
	UpsertUserMetadata(ctx context.Context, data repositoryTypes.UpsertUserMetadata) error
}
