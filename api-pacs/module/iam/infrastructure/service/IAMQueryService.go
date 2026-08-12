package service

import (
	"context"

	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/iam/domain/repository"
)

// IAMQueryService handles the IAM query service logic
type IAMQueryService struct {
	repository.IAMQueryRepositoryInterface
}

// GetSessionToken get session token
func (service *IAMQueryService) GetSessionToken(ctx context.Context, key string) (entity.TokenSession, error) {
	session, err := service.IAMQueryRepositoryInterface.GetTokenSession(key)
	if err != nil {
		return entity.TokenSession{}, err
	}

	return session, nil
}

func (service *IAMQueryService) IsUserSuspended(_ context.Context, tenantID, userID string) (bool, error) {
	return service.IAMQueryRepositoryInterface.IsUserSuspended(tenantID, userID)
}
