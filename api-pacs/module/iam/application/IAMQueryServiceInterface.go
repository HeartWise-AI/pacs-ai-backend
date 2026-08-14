package application

import (
	"context"

	"api-pacs/module/iam/domain/entity"
)

// IAMQueryServiceInterface holds the implementable methods for the iam query service
type IAMQueryServiceInterface interface {
	GetSessionToken(ctx context.Context, key string) (entity.TokenSession, error)
	IsUserSuspended(ctx context.Context, tenantID, userID string) (bool, error)
}
