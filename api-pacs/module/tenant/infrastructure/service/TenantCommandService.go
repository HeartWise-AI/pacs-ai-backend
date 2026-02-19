package service

import (
	"context"

	"github.com/segmentio/ksuid"

	"api-pacs/module/tenant/domain/repository"
	repositoryTypes "api-pacs/module/tenant/infrastructure/repository/types"
	"api-pacs/module/tenant/infrastructure/service/types"
)

// TenantCommandService handles the Tenant command service logic
type TenantCommandService struct {
	repository.TenantCommandRepositoryInterface
}

// AddOnboardingQuestionnaireAnswer adds an onboarding questionnaire answer
func (service *TenantCommandService) AddOnboardingQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error {
	// add onboarding questionnaire answers
	for _, answer := range data.OnboardingQuestionnaireAnswers {
		err := service.TenantCommandRepositoryInterface.InsertOnboardingQuestionnaireAnswer(ctx, repositoryTypes.AddOnboardingQuestionnaireAnswer{
			ID:                     generateID(),
			TenantID:               data.TenantID,
			UserID:                 data.UserID,
			QuestionnaireType:      data.QuestionnaireType,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// RemoveOnboardingQuestionnaireAnswer removes an onboarding questionnaire answer
func (service *TenantCommandService) RemoveOnboardingQuestionnaireAnswer(ctx context.Context, ID string) error {
	err := service.TenantCommandRepositoryInterface.DeleteOnboardingQuestionnaireAnswer(ctx, ID)
	if err != nil {
		return err
	}

	return nil
}

func generateID() string {
	return ksuid.New().String()
}
