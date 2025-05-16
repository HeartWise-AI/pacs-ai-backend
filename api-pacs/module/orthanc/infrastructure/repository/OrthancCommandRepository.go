package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/domain/entity"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepository handles the orthanc command repository logic
type OrthancCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// UpsertDICOMModality upsert DICOM modality
func (repository *OrthancCommandRepository) UpsertDICOMModality(ctx context.Context, data repositoryTypes.UpsertDICOMModality) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	var dicomModality entity.DICOMModality

	collectionPath := fmt.Sprintf("%s/%s", dicomModality.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	// creates a new record if modality id is not existing, if it exists updates instead
	_, err = docRef.Create(ctx, entity.DICOMModality{
		ID:            data.ID,
		TenantID:      data.TenantID,
		AET:           data.AET,
		HostHash:      data.HostHash,
		CFindEnabled:  data.CFindEnabled,
		CMoveEnabled:  data.CMoveEnabled,
		CStoreEnabled: data.CStoreEnabled,
		CreatedAt:     int(time.Now().Unix()),
		UpdatedAt:     int(time.Now().Unix()),
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			// update dicom modality
			_, err = docRef.Set(ctx, entity.DICOMModality{
				ID:            data.ID,
				TenantID:      data.TenantID,
				AET:           data.AET,
				HostHash:      data.HostHash,
				CFindEnabled:  data.CFindEnabled,
				CMoveEnabled:  data.CMoveEnabled,
				CStoreEnabled: data.CStoreEnabled,
				UpdatedAt:     int(time.Now().Unix()),
			})
			if err != nil {
				log.Println(err)
				return errors.New(apiError.FirestoreError)
			}

			return nil
		}
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}
