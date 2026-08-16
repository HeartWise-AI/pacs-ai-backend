package application

import (
	"context"

	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandServiceInterface holds the implementable methods for the user command service
type UserCommandServiceInterface interface {
	// AcceptPolicies records acceptance of every current required policy.
	AcceptPolicies(ctx context.Context, data types.AcceptPolicies) error
	// ChangeTenantUserAccess suspends or reactivates a tenant user.
	ChangeTenantUserAccess(ctx context.Context, data types.ChangeTenantUserAccess) error
	// CreateTenantUser creates a tenant user
	CreateTenantUser(ctx context.Context, data types.CreateTenantUser) (string, error)
	// DeleteTenantUser deletes a tenant user
	DeleteTenantUser(ctx context.Context, data types.DeleteTenantUser) error
	// DeleteTenantUserEmailInvite deletes a tenant user email invite
	DeleteTenantUserEmailInvite(ctx context.Context, ID string) error
	// ResetTutorial resets the tutorial for a user
	ResetTutorial(ctx context.Context, data types.ResetTutorial) error
	// ResendTenantUserEmailInvite resends a tenant user email invite
	ResendTenantUserEmailInvite(ctx context.Context, data types.ResendTenantUserEmailInvite) error
	// RegisterTenantUser registers a tenant user
	RegisterTenantUser(ctx context.Context, data types.RegisterTenantUser) error
	// SendTenantUserEmailInvite sends a tenant user email invite
	SendTenantUserEmailInvite(ctx context.Context, data types.SendTenantUserEmailInvite) error
	// UpdateTenantUser updates a tenant user
	UpdateTenantUser(ctx context.Context, data types.UpdateTenantUser) error
	// UpdateTenantUserPassword updates a tenant user's password
	UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error
	// UpdateUserMetadata updates a user metadata
	UpdateUserMetadata(ctx context.Context, data types.UpdateUserMetadata) error
}
