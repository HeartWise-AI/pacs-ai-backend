package service

import (
	"context"
	"log"

	"google.golang.org/api/iterator"

	"api-iam/infrastructures/providers/sdk/firebaseadmin"
	"api-iam/module/auth/infrastructure/service/types"
)

// AuthQueryService handles the Auth query service logic
type AuthQueryService struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// GetTenantUserByID get tenant user by id
func (service *AuthCommandService) GetTenantUserByID(ctx context.Context, tenantID, uid string) (types.GetTenantUser, error) {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return types.GetTenantUser{}, err
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return types.GetTenantUser{}, err
	}

	user, err := tenantAuth.GetUser(ctx, uid)
	if err != nil {
		log.Println(err)
		return types.GetTenantUser{}, err
	}

	return types.GetTenantUser{
		UID:               user.UID,
		Email:             user.Email,
		Name:              user.DisplayName,
		IsEmailVerified:   user.EmailVerified,
		IsAccountDisabled: user.Disabled,
		TenantID:          user.CustomClaims[types.TenantClaim].(string),
		Role:              user.CustomClaims[types.RoleClaim].(string),
	}, nil
}

// GetTenantUsers get tenant users
func (service *AuthCommandService) GetTenantUsers(ctx context.Context, tenantID string) ([]types.GetTenantUser, error) {
	firebaseAuth, err := service.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return []types.GetTenantUser{}, err
	}

	// get tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return []types.GetTenantUser{}, err
	}

	var users []types.GetTenantUser

	iter := tenantAuth.Users(ctx, "")
	for {
		user, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Println(err)
			return []types.GetTenantUser{}, err
		}

		users = append(users, types.GetTenantUser{
			UID:               user.UID,
			Email:             user.Email,
			Name:              user.DisplayName,
			IsEmailVerified:   user.EmailVerified,
			IsAccountDisabled: user.Disabled,
			TenantID:          user.CustomClaims[types.TenantClaim].(string),
			Role:              user.CustomClaims[types.RoleClaim].(string),
		})
	}

	return users, nil
}
