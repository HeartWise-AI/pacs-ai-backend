package repository

import (
	"context"
	"errors"
	"log"
	"sync"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/infrastructure/repository/types"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
)

// UserQueryRepository handles the user query repository logic
type UserQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectTenantUserByID get tenant user by id
func (repository *UserQueryRepository) SelectTenantUserByID(ctx context.Context, tenantID, id string) (repositoryTypes.GetTenantUser, error) {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	// get firebase auth user
	authUser, err := tenantAuth.GetUser(ctx, id)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// get firestore user
	var user entity.User

	firestoreRes, err := firestoreClient.Collection(user.GetModelName()).Doc(id).Get(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&user)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	return repositoryTypes.GetTenantUser{
		ID:                authUser.UID,
		TenantID:          authUser.TenantID,
		Role:              user.Role,
		Name:              authUser.DisplayName,
		Email:             authUser.Email,
		LicenseNo:         user.LicenseNo,
		Specialty:         user.Specialty,
		IsEmailVerified:   authUser.EmailVerified,
		IsAccountDisabled: authUser.Disabled,
		CreatedAt:         uint(user.CreatedAt),
		UpdatedAt:         uint(user.UpdatedAt),
	}, nil
}

// SelectTenantUsers get tenant users
func (repository *UserQueryRepository) SelectTenantUsers(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenantUser, error) {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return []repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return []repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return []repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	users := []types.GetTenantUser{} // empty
	var rw = sync.RWMutex{}
	eg, egCtx := errgroup.WithContext(ctx)

	iter := tenantAuth.Users(ctx, "")

	for {
		authUser, err := iter.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			log.Println(err)
			return []types.GetTenantUser{}, err
		}

		rw.Lock()

		eg.Go(func() error {
			defer rw.Unlock()

			var user entity.User
			doc, err := firestoreClient.Collection(user.GetModelName()).Doc(authUser.UID).Get(egCtx)
			if err != nil {
				log.Println(err)
				return err
			}

			err = doc.DataTo(&user)
			if err != nil {
				log.Println(err)
				return err
			}

			users = append(users, types.GetTenantUser{
				ID:                authUser.UID,
				TenantID:          user.TenantID,
				Role:              user.Role,
				Name:              authUser.DisplayName,
				Email:             authUser.Email,
				LicenseNo:         user.LicenseNo,
				Specialty:         user.Specialty,
				IsEmailVerified:   authUser.EmailVerified,
				IsAccountDisabled: authUser.Disabled,
				CreatedAt:         uint(user.CreatedAt),
				UpdatedAt:         uint(user.UpdatedAt),
			})

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return []types.GetTenantUser{}, err
	}

	if len(users) == 0 {
		return []types.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}

	return users, nil
}
