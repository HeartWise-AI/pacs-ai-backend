package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"
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

	// insert new record else update if existing
	_, err = docRef.Create(ctx, entity.DICOMModality{
		ID:            data.ID,
		TenantID:      data.TenantID,
		ModalityID:    data.ModalityID,
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
			updateDICOMModality := []firestore.Update{
				{
					Path:  "aet",
					Value: data.AET,
				},
				{
					Path:  "host_hash",
					Value: data.HostHash,
				},
				{
					Path:  "c_find_enabled",
					Value: data.CFindEnabled,
				},
				{
					Path:  "c_move_enabled",
					Value: data.CMoveEnabled,
				},
				{
					Path:  "c_store_enabled",
					Value: data.CStoreEnabled,
				},
				{
					Path:  "updated_at",
					Value: int(time.Now().Unix()),
				},
			}

			_, err = docRef.Update(ctx, updateDICOMModality)
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
