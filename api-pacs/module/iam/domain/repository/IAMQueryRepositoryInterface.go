package repository

import (
	"api-pacs/module/iam/domain/entity"
)

type IAMQueryRepositoryInterface interface {
	GetTokenSession(key string) (entity.TokenSession, error)
	IsUserSuspended(tenantID, userID string) (bool, error)
}
