package repository

import (
	"context"
	"errors"
	"fmt"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/domain/entity"
)

// OrthancQueryRepository handles the orthanc query repository logic
type OrthancQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectDICOMModalityByModalityID get DICOM modality by modality id
func (repository *OrthancQueryRepository) SelectDICOMModalityByModalityID(ctx context.Context, tenantID, modalityID string) (entity.DICOMModality, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	// get firestore DICOM modality
	var dicomModality entity.DICOMModality

	ID := fmt.Sprintf("%s:%s", tenantID, modalityID)
	firestoreRes, err := firestoreClient.Collection(dicomModality.GetModelName()).Doc(ID).Get(ctx)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&dicomModality)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	return dicomModality, nil
}
