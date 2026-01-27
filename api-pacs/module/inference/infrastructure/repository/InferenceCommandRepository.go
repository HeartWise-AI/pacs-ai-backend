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

// DeleteModelFeedback deletes model feedback
func (repository *InferenceCommandRepository) DeleteModelFeedback(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete model feedback
	var model entity.ModelFeedback

	collectionPath := fmt.Sprintf("%s/%s", model.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertModelFeedbackAnswer inserts a new  model feedback answer
func (repository *InferenceCommandRepository) InsertModelFeedbackAnswer(ctx context.Context, data types.AddModelFeedbackAnswer) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// add model feedback answer
	model := entity.ModelFeedbackAnswer{
		ID:                     data.ID,
		ModelFeedbackID:        data.ModelFeedbackID,
		QuestionnaireID:        data.QuestionnaireID,
		QuestionnaireQuestions: data.QuestionnaireQuestions,
		QuestionnaireAnswers:   data.QuestionnaireAnswers,
		CreatedAt:              int(time.Now().Unix()),
		UpdatedAt:              int(time.Now().Unix()),
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
		ID:                  data.ID,
		TenantID:            data.TenantID,
		ContainerID:         data.ContainerID,
		Name:                data.Name,
		DockerImage:         data.DockerImage,
		Envs:                data.Envs,
		DisallowedDICOMTags: data.DisallowedDICOMTags,
		OutputMode:          data.OutputMode,
		CreatedAt:           int(time.Now().Unix()),
		UpdatedAt:           int(time.Now().Unix()),
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
			Path:  "disallowed_dicom_tags",
			Value: data.DisallowedDICOMTags,
		},
		{
			Path:  "output_mode",
			Value: data.OutputMode,
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

// UpsertModelFeedback upserts model feedback
func (repository *InferenceCommandRepository) UpsertModelFeedback(ctx context.Context, data types.UpsertModelFeedback) error {
	var modelFeedback entity.ModelFeedback

	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	collectionPath := fmt.Sprintf("%s/%s", modelFeedback.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	// try to insert model feedback
	_, err = docRef.Create(ctx, entity.ModelFeedback{
		ID:           data.ID,
		TenantID:     data.TenantID,
		UserID:       data.UserID,
		ModelID:      data.ModelID,
		FeedbackType: data.FeedbackType,
		CreatedAt:    int(time.Now().Unix()),
		UpdatedAt:    int(time.Now().Unix()),
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			// update model feedback
			updateModelFeedback := []firestore.Update{
				{
					Path:  "model_id",
					Value: data.ModelID,
				},
				{
					Path:  "feedback_type",
					Value: data.FeedbackType,
				},
				{
					Path:  "updated_at",
					Value: int(time.Now().Unix()),
				},
			}

			_, err = docRef.Update(ctx, updateModelFeedback)
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
