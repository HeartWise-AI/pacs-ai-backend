package repository

import (
	"context"
	"errors"
	"log"

	"google.golang.org/api/iterator"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/domain/entity"
	"api-pacs/module/tenant/infrastructure/repository/types"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantQueryRepository handles the tenant query repository logic
type TenantQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectTenants get tenants
func (repository *TenantQueryRepository) SelectTenants(ctx context.Context, tenantID string) ([]repositoryTypes.GetTenant, error) {
	firebaseAuth, err := repository.FirebaseAdminSDK.App.Auth(ctx)
	if err != nil {
		log.Println(err)
		return []repositoryTypes.GetTenant{}, errors.New(apiError.FirebaseAuthError)
	}

	// tenant auth
	tenantAuth, err := firebaseAuth.TenantManager.AuthForTenant(tenantID)
	if err != nil {
		log.Println(err)
		return []repositoryTypes.GetTenant{}, errors.New(apiError.FirebaseAuthError)
	}

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return []repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	tenants := []types.GetTenant{}

	iter := tenantAuth.Users(ctx, "")
	for {
		authUser, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Println(err)
			return []types.GetTenant{}, err
		}

		var tenant entity.Tenant
		doc, err := firestoreClient.Collection(tenant.GetModelName()).Doc(tenantID).Get(ctx)
		if err != nil {
			log.Println(err)
			return []types.GetTenant{}, err
		}

		err = doc.DataTo(&tenant)
		if err != nil {
			log.Println(err)
			return []types.GetTenant{}, err
		}

		tenants = append(tenants, types.GetTenant{
			ID:        authUser.UID,
			Name:      tenant.Name,
			Address:   tenant.Address,
			CreatedAt: uint(tenant.CreatedAt),
			UpdatedAt: uint(tenant.UpdatedAt),
		})
	}

	if len(tenants) == 0 {
		return []types.GetTenant{}, errors.New(apiError.MissingRecord)
	}

	return tenants, nil
}
