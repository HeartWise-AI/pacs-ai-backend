package repository

import (
	"context"

	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

type UserCommandRepositoryInterface interface {
	DeleteUser(ctx context.Context, tenantID, id string) error
	InsertUser(ctx context.Context, data repositoryTypes.CreateUser) (string, error)
	UpdateUserPassword(ctx context.Context, data repositoryTypes.UpdateUserPassword) error
}
