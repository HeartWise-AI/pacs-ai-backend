package repository

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/domain/entity"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantQueryRepository handles the tenant query repository logic
type TenantQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectTenantByID get tenant by id
func (repository *TenantQueryRepository) SelectTenantByID(ctx context.Context, tenantID string) (repositoryTypes.GetTenant, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	// get firestore tenant
	var tenant entity.Tenant

	firestoreRes, err := firestoreClient.Collection(tenant.GetModelName()).Doc(tenantID).Get(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&tenant)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	return repositoryTypes.GetTenant{
		ID:              tenantID,
		Name:            tenant.Name,
		Address:         tenant.Address,
		AvailableModels: tenant.AvailableModels,
		CreatedAt:       uint(tenant.CreatedAt),
		UpdatedAt:       uint(tenant.UpdatedAt),
	}, nil
}
