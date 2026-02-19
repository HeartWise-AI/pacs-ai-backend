package repository

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
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

// SelectModelFeedbackByUserModelID get model feedback by model and user ID
func (repository *InferenceQueryRepository) SelectModelFeedbackByUserModelID(ctx context.Context, data types.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return entity.ModelFeedback{}, errors.New(apiError.FirestoreError)
	}

	// get firestore model feedback
	var modelFeedback entity.ModelFeedback

	firestoreRes, err := firestoreClient.Collection(modelFeedback.GetModelName()).Where("tenant_id", "==", data.TenantID).Where("user_id", "==", data.UserID).Where("model_id", "==", data.ModelID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return entity.ModelFeedback{}, errors.New(apiError.FirestoreError)
	}

	if len(firestoreRes) == 0 {
		return entity.ModelFeedback{}, errors.New(apiError.MissingRecord)
	}

	err = firestoreRes[0].DataTo(&modelFeedback)
	if err != nil {
		log.Println(err)
		return entity.ModelFeedback{}, errors.New(apiError.FirestoreError)
	}

	return modelFeedback, nil
}

// SelectModelFeedbackAnswersByFeedbackID get model feedback answers by feedback ID
func (repository *InferenceQueryRepository) SelectModelFeedbackAnswersByFeedbackID(ctx context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore model feedback answer
	var modelFeedbackAnswer entity.ModelFeedbackAnswer

	query := firestoreClient.Collection(modelFeedbackAnswer.GetModelName()).Where("model_feedback_id", "==", feedbackID)
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var modelFeedbackAnswers []entity.ModelFeedbackAnswer

	for _, doc := range docs {
		var modelFeedbackAnswer entity.ModelFeedbackAnswer
		if err := doc.DataTo(&modelFeedbackAnswer); err != nil {
			log.Println(err)
			continue
		}

		modelFeedbackAnswer.ID = doc.Ref.ID
		modelFeedbackAnswers = append(modelFeedbackAnswers, modelFeedbackAnswer)
	}

	if len(modelFeedbackAnswers) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return modelFeedbackAnswers, nil
}

// SelectOnboardingModelQuestionnaireAnswers selects onboarding model questionnaire answers
func (repository *InferenceQueryRepository) SelectOnboardingModelQuestionnaireAnswers(ctx context.Context, data types.GetOnboardingModelQuestionnaireAnswer) ([]entity.OnboardingModelQuestionnaireAnswer, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore onboarding model questionnaire answers
	var onboardingModelQuestionnaireAnswer entity.OnboardingModelQuestionnaireAnswer

	query := firestoreClient.Collection(onboardingModelQuestionnaireAnswer.GetModelName()).Where("tenant_id", "==", data.TenantID).Where("user_id", "==", data.UserID)

	// if model id is set
	if data.ModelID != nil {
		query = query.Where("model_id", "==", data.ModelID)
	}

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var onboardingModelQuestionnaireAnswers []entity.OnboardingModelQuestionnaireAnswer

	for _, doc := range docs {
		var onboardingModelQuestionnaireAnswer entity.OnboardingModelQuestionnaireAnswer
		if err := doc.DataTo(&onboardingModelQuestionnaireAnswer); err != nil {
			log.Println(err)
			continue
		}

		onboardingModelQuestionnaireAnswer.ID = doc.Ref.ID
		onboardingModelQuestionnaireAnswers = append(onboardingModelQuestionnaireAnswers, onboardingModelQuestionnaireAnswer)
	}

	if len(onboardingModelQuestionnaireAnswers) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return onboardingModelQuestionnaireAnswers, nil
}
