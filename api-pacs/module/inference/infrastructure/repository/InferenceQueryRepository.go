package repository

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
)

// InferenceQueryRepository handles the inference query repository logic
type InferenceQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectInferenceModelByID get inference model by id
func (repository *InferenceQueryRepository) SelectInferenceModelByID(ctx context.Context, ID string) (entity.InferenceModel, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	// get firestore inference model
	var inferenceModel entity.InferenceModel

	firestoreRes, err := firestoreClient.Collection(inferenceModel.GetModelName()).Doc(ID).Get(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&inferenceModel)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	return inferenceModel, nil
}

// SelectInferenceModelByContainerID get inference model by container
func (repository *InferenceQueryRepository) SelectInferenceModelByContainer(ctx context.Context, tenantID, containerID string) (entity.InferenceModel, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	// get firestore inference model
	var inferenceModel entity.InferenceModel

	firestoreRes, err := firestoreClient.Collection(inferenceModel.GetModelName()).Where("tenant_id", "==", tenantID).Where("container_id", "==", containerID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&inferenceModel)
	if err != nil {
		log.Println(err)
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}

	return inferenceModel, nil
}

// SelectInferenceModels get inference models by tenant id
func (repository *InferenceQueryRepository) SelectInferenceModels(ctx context.Context, tenantID string) ([]entity.InferenceModel, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore inference models
	var inferenceModel entity.InferenceModel

	query := firestoreClient.Collection(inferenceModel.GetModelName()).Where("tenant_id", "==", tenantID)
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var inferenceModels []entity.InferenceModel

	for _, doc := range docs {
		var inferenceModel entity.InferenceModel
		if err := doc.DataTo(&inferenceModel); err != nil {
			log.Println(err)
			continue
		}

		inferenceModel.ID = doc.Ref.ID
		inferenceModels = append(inferenceModels, inferenceModel)
	}

	if len(inferenceModels) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return inferenceModels, nil
}
