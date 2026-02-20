package repository

import (
	"context"
	"errors"
	"log"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/domain/entity"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
)

// TenantQueryRepository handles the tenant query repository logic
type TenantQueryRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

// SelectOnboardingQuestionnaireAnswers selects onboarding questionnaire answers
func (repository *TenantQueryRepository) SelectOnboardingQuestionnaireAnswers(ctx context.Context, data repositoryTypes.GetOnboardingQuestionnaireAnswer) ([]entity.OnboardingQuestionnaireAnswer, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	// get firestore onboarding questionnaire answers
	var onboardingQuestionnaireAnswer entity.OnboardingQuestionnaireAnswer

	query := firestoreClient.Collection(onboardingQuestionnaireAnswer.GetModelName()).Where("tenant_id", "==", data.TenantID).Where("user_id", "==", data.UserID)

	// if questionnaire type is set
	if data.QuestionnaireType != nil {
		query = query.Where("questionnaire_type", "==", data.QuestionnaireType)
	}

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}

	var onboardingQuestionnaireAnswers []entity.OnboardingQuestionnaireAnswer

	for _, doc := range docs {
		var onboardingQuestionnaireAnswer entity.OnboardingQuestionnaireAnswer
		if err := doc.DataTo(&onboardingQuestionnaireAnswer); err != nil {
			log.Println(err)
			continue
		}

		onboardingQuestionnaireAnswer.ID = doc.Ref.ID
		onboardingQuestionnaireAnswers = append(onboardingQuestionnaireAnswers, onboardingQuestionnaireAnswer)
	}

	if len(onboardingQuestionnaireAnswers) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return onboardingQuestionnaireAnswers, nil
}

// SelectTenantByID get tenant by id
func (repository *TenantQueryRepository) SelectTenantByID(ctx context.Context, tenantID string) (repositoryTypes.GetTenant, error) {
	// firestore client
	firestoreClient, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	// get firestore tenant
	var tenant entity.Tenant

	firestoreRes, err := firestoreClient.Collection(tenant.GetModelName()).Doc(tenantID).Get(ctx)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	err = firestoreRes.DataTo(&tenant)
	if err != nil {
		log.Println(err)
		return repositoryTypes.GetTenant{}, errors.New(apiError.FirestoreError)
	}

	return repositoryTypes.GetTenant{
		ID:        tenantID,
		Name:      tenant.Name,
		Address:   tenant.Address,
		CreatedAt: uint(tenant.CreatedAt),
		UpdatedAt: uint(tenant.UpdatedAt),
	}, nil
}
