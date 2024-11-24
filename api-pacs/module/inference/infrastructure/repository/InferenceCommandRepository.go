package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/firestore"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferenceCommandRepository handles the inference command repository logic
type InferenceCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// DeleteInferenceModel deletes an inference model
func (repository *InferenceCommandRepository) DeleteInferenceModel(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete inference model
	var model entity.InferenceModel

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertInferenceModel inserts a new inference model
func (repository *InferenceCommandRepository) InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// add inference model
	model := entity.InferenceModel{
		ID:          data.ID,
		TenantID:    data.TenantID,
		ContainerID: data.ContainerID,
		Name:        data.Name,
		DockerImage: data.DockerImage,
		Envs:        data.Envs,
		OutputMode:  data.OutputMode,
		CreatedAt:   int(time.Now().Unix()),
		UpdatedAt:   int(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, model)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateInferenceModel updates an inference model
func (repository *InferenceCommandRepository) UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// update inference model
	var model entity.InferenceModel

	updateInferenceModel := []firestore.Update{
		{
			Path:  "name",
			Value: data.Name,
		},
		{
			Path:  "docker_image",
			Value: data.DockerImage,
		},
		{
			Path:  "envs",
			Value: data.Envs,
		},
		{
			Path:  "output_mode",
			Value: data.OutputMode,
		},
		{
			Path:  "created_at",
			Value: int(time.Now().Unix()),
		},
		{
			Path:  "updated_at",
			Value: int(time.Now().Unix()),
		},
	}

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateInferenceModel)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateInferenceModelContainerID updates the container ID of an inference model
func (repository *InferenceCommandRepository) UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// update inference model
	var model entity.InferenceModel

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	updateInferenceModel := []firestore.Update{
		{
			Path:  "container_id",
			Value: containerID,
		},
		{
			Path:  "updated_at",
			Value: int(time.Now().Unix()),
		},
	}

	_, err = docRef.Update(ctx, updateInferenceModel)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}
