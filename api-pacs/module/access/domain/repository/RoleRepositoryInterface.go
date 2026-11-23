package repository

import (
	"context"

	"api-pacs/module/access/domain/entity"
)

// UserRoleAssignment is the base role and custom role stored on a user.
type UserRoleAssignment struct {
	UserID       string
	TenantID     string
	Role         string
	CustomRoleID string
}

// RoleRepositoryInterface stores tenant custom roles and their assignment.
// Every method is tenant-scoped; records of another tenant read as missing.
type RoleRepositoryInterface interface {
	ListRoles(ctx context.Context, tenantID string) ([]entity.CustomRole, error)
	GetRole(ctx context.Context, tenantID, roleID string) (entity.CustomRole, error)
	CreateRole(ctx context.Context, role entity.CustomRole) error
	// UpdateRole fails with MissingRecord when the role no longer exists.
	UpdateRole(ctx context.Context, role entity.CustomRole) error
	// DeleteRole fails with ROLE_IN_USE while a user holds the role; it is
	// atomic with SetUserCustomRole.
	DeleteRole(ctx context.Context, tenantID, roleID string) error
	GetUserAssignment(ctx context.Context, tenantID, userID string) (UserRoleAssignment, error)
	ListUserAssignments(ctx context.Context, tenantID string) ([]UserRoleAssignment, error)
	// SetUserCustomRole assigns ("" clears) a role of the tenant to a USER
	// account: MissingRecord for an unknown user or role, InvalidPayload for
	// a non-USER account.
	SetUserCustomRole(ctx context.Context, tenantID, userID, roleID string) error
}
