package service

import (
	"context"

	"api-pacs/module/user/domain/repository"
	"api-pacs/module/user/infrastructure/service/types"
)

// UserQueryService handles the User query service logic
type UserQueryService struct {
	repository.UserQueryRepositoryInterface
}

// GetTenantUsers get tenant users
func (service *UserQueryService) GetTenantUsers(ctx context.Context, tenantID string) ([]types.GetTenantUser, error) {
	res, err := service.UserQueryRepositoryInterface.SelectTenantUsers(ctx, tenantID)
	if err != nil {
		return []types.GetTenantUser{}, err
	}

	var users []types.GetTenantUser

	for _, user := range res {
		users = append(users, types.GetTenantUser{
			ID:                user.ID,
			TenantID:          user.TenantID,
			Role:              user.Role,
			Name:              user.Name,
			Email:             user.Email,
			LicenseNo:         user.LicenseNo,
			Specialty:         user.Specialty,
			IsEmailVerified:   user.IsEmailVerified,
			IsAccountDisabled: user.IsAccountDisabled,
			CreatedAt:         user.CreatedAt,
			UpdatedAt:         user.UpdatedAt,
		})

	}

	return users, nil
}

// GetTenantUserByID get tenant user by id
func (service *UserQueryService) GetTenantUserByID(ctx context.Context, tenantID, id string) (types.GetTenantUser, error) {
	user, err := service.UserQueryRepositoryInterface.SelectTenantUserByID(ctx, tenantID, id)
	if err != nil {
		return types.GetTenantUser{}, err
	}

	return types.GetTenantUser{
		ID:                user.ID,
		TenantID:          user.TenantID,
		Role:              user.Role,
		Name:              user.Name,
		Email:             user.Email,
		LicenseNo:         user.LicenseNo,
		Specialty:         user.Specialty,
		IsEmailVerified:   user.IsEmailVerified,
		IsAccountDisabled: user.IsAccountDisabled,
		CreatedAt:         uint(user.CreatedAt),
		UpdatedAt:         uint(user.UpdatedAt),
	}, nil
}
