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

// SelectDICOMModalityByID get DICOM modality by id
func (repository *OrthancQueryRepository) SelectDICOMModalityByID(ctx context.Context, ID, tenantID string) (entity.DICOMModality, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.DICOMModality{}, errors.New(apiError.FirestoreError)
	}

	// get firestore DICOM modality
	var dicomModality entity.DICOMModality

	firestoreRes, err := firestoreClient.Collection(dicomModality.GetModelName()).Doc(ID).Get(ctx)
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
