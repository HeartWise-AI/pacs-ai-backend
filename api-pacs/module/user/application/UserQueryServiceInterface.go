package application

import (
	"context"

	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserQueryServiceInterface holds the implementable methods for the user query service
type UserQueryServiceInterface interface {
	GetDoctorSpecialties(ctx context.Context) ([]map[string]interface{}, error)
	GetTenantUserByID(ctx context.Context, tenantID, id string) (types.GetTenantUser, error)
	GetTenantUsers(ctx context.Context, tenantID string) ([]types.GetTenantUser, error)
	GetUserMetadata(ctx context.Context, userID string) (entity.UserMetadata, error)
}
