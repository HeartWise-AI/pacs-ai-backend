package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/segmentio/ksuid"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/domain/entity"
	repositoryTypes "api-pacs/module/orthanc/infrastructure/repository/types"
)

// OrthancCommandRepository handles the orthanc command repository logic
type OrthancCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// DeleteDICOMModality deletes a dicom modality
func (repository *OrthancCommandRepository) DeleteDICOMModality(ctx context.Context, tenantID, modalityID string) error {
	var model entity.DICOMModality

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// query to get the document ID first
	query := firestoreClient.Collection(model.GetModelName()).Where("tenant_id", "==", tenantID).Where("modality_id", "==", modalityID).Limit(1)
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	if len(docs) > 0 {
		collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), docs[0].Ref.ID)
		docRef := firestoreClient.Doc(collectionPath)

		_, err = docRef.Delete(ctx)
		if err != nil {
			log.Println(err)
			return errors.New(apiError.FirestoreError)
		}
	}

	return nil
}

// UpsertDICOMModality upsert DICOM modality
func (repository *OrthancCommandRepository) UpsertDICOMModality(ctx context.Context, data repositoryTypes.UpsertDICOMModality) error {
	var dicomModality entity.DICOMModality

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	if data.ID != nil {
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

		collectionPath := fmt.Sprintf("%s/%s", dicomModality.GetModelName(), *data.ID)
		docRef := firestoreClient.Doc(collectionPath)

		_, err = docRef.Update(ctx, updateDICOMModality)
		if err != nil {
			log.Println(err)
			return errors.New(apiError.FirestoreError)
		}

		return nil
	}

	// insert dicom modality
	collectionPath := fmt.Sprintf("%s/%s", dicomModality.GetModelName(), generateID())
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, entity.DICOMModality{
		ID:            *data.ID,
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
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

func generateID() string {
	return ksuid.New().String()
}
