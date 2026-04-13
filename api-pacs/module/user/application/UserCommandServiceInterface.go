package application

import (
	"context"

	"api-pacs/module/user/infrastructure/service/types"
)

// UserCommandServiceInterface holds the implementable methods for the user command service
type UserCommandServiceInterface interface {
	CreateTenantUser(ctx context.Context, data types.CreateTenantUser) (string, error)
	DeleteTenantUser(ctx context.Context, tenantID, id string) error
	ResetTutorial(ctx context.Context, data types.ResetTutorial) error
	ResendTenantUserEmailInvite(ctx context.Context, data types.ResendTenantUserEmailInvite) error
	SendTenantUserEmailInvite(ctx context.Context, data types.SendTenantUserEmailInvite) error
	UpdateTenantUser(ctx context.Context, data types.UpdateTenantUser) error
	UpdateTenantUserPassword(ctx context.Context, data types.UpdateTenantUserPassword) error
	UpdateUserMetadata(ctx context.Context, data types.UpdateUserMetadata) error
}
