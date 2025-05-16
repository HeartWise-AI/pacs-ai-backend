package repository

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/domain/entity"
)

// OrthancQueryRepository handles the orthanc query repository logic
type OrthancQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectDICOMModalityByTenantModality get DICOM modality by tenant and modality
func (repository *OrthancQueryRepository) SelectDICOMModalityByTenantModality(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	// get firestore DICOM modality
	var dicomModality entity.DICOMModality

	firestoreRes, err := firestoreClient.Collection(dicomModality.GetModelName()).Where("tenant_id", "==", tenantID).Where("id", "==", modalityID).Limit(1).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return entity.DICOMModality{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&dicomModality)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	return dicomModality, nil
}
