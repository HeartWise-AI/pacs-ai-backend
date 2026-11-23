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
	SaveRole(ctx context.Context, role entity.CustomRole) error
	DeleteRole(ctx context.Context, tenantID, roleID string) error
	GetUserAssignment(ctx context.Context, tenantID, userID string) (UserRoleAssignment, error)
	ListUserAssignments(ctx context.Context, tenantID string) ([]UserRoleAssignment, error)
	SetUserCustomRole(ctx context.Context, tenantID, userID, roleID string) error
}
