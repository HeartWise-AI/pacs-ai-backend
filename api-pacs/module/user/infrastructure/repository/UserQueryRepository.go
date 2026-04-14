package repository

import (
	"context"
	"errors"
	"log"
	"sync"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/domain/entity"
	"api-pacs/module/user/infrastructure/repository/types"
	repositoryTypes "api-pacs/module/user/infrastructure/repository/types"
)

// UserQueryRepository handles the user query repository logic
type UserQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectTenantUserByEmail get tenant user by email
func (repository *UserQueryRepository) SelectTenantUserByEmail(ctx context.Context, tenantID, email string) (repositoryTypes.GetTenantUser, error) {
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

	// get firebase auth user by email
	authUser, err := tenantAuth.GetUserByEmail(ctx, email)
	if err != nil {
		log.Println(err)
		if status.Code(err) == codes.Unknown {
			return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
		}
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirebaseAuthError)
	}

	// get firestore user
	var user entity.User

	firestoreRes, err := firestoreClient.Collection(user.GetModelName()).Where("tenant_id", "==", tenantID).Where("email", "==", email).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return repositoryTypes.GetTenantUser{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&user)
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
		IsConsentSigned:   user.IsConsentSigned,
		CreatedAt:         uint(user.CreatedAt),
		UpdatedAt:         uint(user.UpdatedAt),
	}, nil
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
		IsConsentSigned:   user.IsConsentSigned,
		CreatedAt:         uint(user.CreatedAt),
		UpdatedAt:         uint(user.UpdatedAt),
	}, nil
}

// SelectTenantUserEmailInviteByEmail get tenant user email invite by email
func (repository *UserQueryRepository) SelectTenantUserEmailInviteByEmail(ctx context.Context, tenantID, email string) (entity.UserEmailInvite, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.UserEmailInvite{}, errors.New(apiError.FirestoreError)
	}

	// query tenant user email invite by email
	var userEmailInvite entity.UserEmailInvite

	firestoreRes, err := firestoreClient.Collection(userEmailInvite.GetModelName()).Where("tenant_id", "==", tenantID).Where("email", "==", email).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return entity.UserEmailInvite{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return entity.UserEmailInvite{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&userEmailInvite)
	if err != nil {
		log.Println(err)
		return entity.UserEmailInvite{}, errors.New(apiError.FirestoreError)
	}

	return userEmailInvite, nil
}

// SelectTenantUserEmailInviteByID get tenant user email invite by id
func (repository *UserQueryRepository) SelectTenantUserEmailInviteByID(ctx context.Context, tenantID, ID string) (entity.UserEmailInvite, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.UserEmailInvite{}, errors.New(apiError.FirestoreError)
	}

	// query tenant user email invite by id
	var userEmailInvite entity.UserEmailInvite

	firestoreRes, err := firestoreClient.Collection(userEmailInvite.GetModelName()).Doc(ID).Get(ctx)
	if err != nil {
		log.Println(err)
		return entity.UserEmailInvite{}, errors.New(apiError.FirestoreError)
	}

	if !firestoreRes.Exists() {
		return entity.UserEmailInvite{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes.DataTo(&userEmailInvite)
	if err != nil {
		log.Println(err)
		return entity.UserEmailInvite{}, errors.New(apiError.FirestoreError)
	}

	return userEmailInvite, nil
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
	var m = sync.Mutex{}
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

		eg.Go(func() error {
			m.Lock()
			defer m.Unlock()

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
				IsConsentSigned:   user.IsConsentSigned,
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

// SelectUserMetadataByID get user metadata by id
func (repository *UserQueryRepository) SelectUserMetadataByID(ctx context.Context, userID string) (entity.UserMetadata, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.UserMetadata{}, errors.New(apiError.FirestoreError)
	}

	// get firestore user metadata
	var userMetadata entity.UserMetadata

	firestoreRes, err := firestoreClient.Collection(userMetadata.GetModelName()).Doc(userID).Get(ctx)
	if err != nil {
		log.Println(err)
		if status.Code(err) == codes.NotFound {
			return entity.UserMetadata{}, errors.New(apiError.MissingRecord)
		}
		return entity.UserMetadata{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&userMetadata)
	if err != nil {
		log.Println(err)
		return entity.UserMetadata{}, errors.New(apiError.FirestoreError)
	}

	return userMetadata, nil
}
