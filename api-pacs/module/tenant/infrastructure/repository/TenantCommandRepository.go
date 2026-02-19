package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/domain/entity"
	"api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantCommandRepository handles the tenant command repository logic
type TenantCommandRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// DeleteOnboardingQuestionnaireAnswer deletes an onboarding questionnaire answer
func (repository *TenantCommandRepository) DeleteOnboardingQuestionnaireAnswer(ctx context.Context, ID string) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// delete onboarding questionnaire answer
	var answer entity.OnboardingQuestionnaireAnswer

	collectionPath := fmt.Sprintf("%s/%s", answer.GetModelName(), ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Delete(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// InsertOnboardingQuestionnaireAnswer inserts a onboarding questionnaire answer
func (repository *TenantCommandRepository) InsertOnboardingQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// add onboarding questionnaire answer
	answer := entity.OnboardingQuestionnaireAnswer{
		ID:                     data.ID,
		TenantID:               data.TenantID,
		UserID:                 data.UserID,
		QuestionnaireType:      data.QuestionnaireType,
		QuestionnaireID:        data.QuestionnaireID,
		QuestionnaireQuestion:  data.QuestionnaireQuestion,
		QuestionnaireAnswerIDs: data.QuestionnaireAnswerIDs,
		QuestionnaireAnswers:   data.QuestionnaireAnswers,
		CreatedAt:              int(time.Now().Unix()),
		UpdatedAt:              int(time.Now().Unix()),
	}

	collectionPath := fmt.Sprintf("%s/%s", answer.GetModelName(), data.ID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Create(ctx, answer)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}
