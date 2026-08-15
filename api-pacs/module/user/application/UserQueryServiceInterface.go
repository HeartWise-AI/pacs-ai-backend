package application

import (
	"context"

	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserQueryServiceInterface holds the implementable methods for the user query service
type UserQueryServiceInterface interface {
	// GetRegistrationPolicies returns current policy metadata before authentication.
	GetRegistrationPolicies(ctx context.Context, tenantID string) ([]types.PolicyDefinition, error)
	// GetPolicyStatus returns current policy acceptance state for one tenant user.
	GetPolicyStatus(ctx context.Context, tenantID, userID string) (types.PolicyStatus, error)
	// GetDoctorSpecialties gets doctor specialties
	GetDoctorSpecialties(ctx context.Context) ([]map[string]interface{}, error)
	// GetTenantUserByID gets tenant user by id
	GetTenantUserByID(ctx context.Context, tenantID, id string) (types.GetTenantUser, error)
	// GetTenantUsers gets tenant users
	GetTenantUsers(ctx context.Context, tenantID string) ([]types.GetTenantUser, error)
	// GetTenantUserEmailInvites gets tenant user email invites
	GetTenantUserEmailInvites(ctx context.Context, tenantID string) ([]entity.UserEmailInvite, error)
	// GetUserMetadata gets user metadata
	GetUserMetadata(ctx context.Context, userID string) (entity.UserMetadata, error)
}
