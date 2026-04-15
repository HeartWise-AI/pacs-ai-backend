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

// UpdateOnboardingEnableConsent updates the onboarding enable consent
func (repository *TenantCommandRepository) UpdateOnboardingEnableConsent(ctx context.Context, data types.UpdateOnboardingEnableConsent) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// update onboarding enable consent
	var tenant entity.Tenant

	// update onboarding configs in firestore
	updateEnableConsent := []firestore.Update{
		{Path: "onboarding_enable_consent", Value: data.OnboardingEnableConsent},
		{Path: "updated_at", Value: int(time.Now().Unix())},
	}

	collectionPath := fmt.Sprintf("%s/%s", tenant.GetModelName(), data.TenantID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateEnableConsent)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}

// UpdateOnboardingEnableRegistration updates the onboarding enable registration
func (repository *TenantCommandRepository) UpdateOnboardingEnableRegistration(ctx context.Context, data types.UpdateOnboardingEnableRegistration) error {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	// update onboarding enable registration
	var tenant entity.Tenant

	// update onboarding configs in firestore
	updateEnableRegistration := []firestore.Update{
		{Path: "onboarding_enable_registration", Value: data.OnboardingEnableRegistration},
		{Path: "updated_at", Value: int(time.Now().Unix())},
	}

	collectionPath := fmt.Sprintf("%s/%s", tenant.GetModelName(), data.TenantID)
	docRef := firestoreClient.Doc(collectionPath)

	_, err = docRef.Update(ctx, updateEnableRegistration)
	if err != nil {
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}

	return nil
}
