package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apiError "api-pacs/internal/errors"
	inferenceApplication "api-pacs/module/inference/application"
	inferenceEntity "api-pacs/module/inference/domain/entity"
	inferenceTypes "api-pacs/module/inference/infrastructure/service/types"
	tenantApplication "api-pacs/module/tenant/application"
	tenantEntity "api-pacs/module/tenant/domain/entity"
	tenantTypes "api-pacs/module/tenant/infrastructure/service/types"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

type tutorialResetTenantQuery struct {
	tenantApplication.TenantQueryServiceInterface
	answers []tenantEntity.OnboardingQuestionnaireAnswer
	err     error
	request tenantTypes.GetOnboardingQuestionnaireAnswer
}

func (query *tutorialResetTenantQuery) GetOnboardingQuestionnaireAnswers(
	_ context.Context,
	data tenantTypes.GetOnboardingQuestionnaireAnswer,
) ([]tenantEntity.OnboardingQuestionnaireAnswer, error) {
	query.request = data
	return query.answers, query.err
}

type tutorialResetTenantCommand struct {
	tenantApplication.TenantCommandServiceInterface
	removedIDs []string
	errByID    map[string]error
}

func (command *tutorialResetTenantCommand) RemoveOnboardingQuestionnaireAnswer(_ context.Context, ID string) error {
	command.removedIDs = append(command.removedIDs, ID)
	return command.errByID[ID]
}

type tutorialResetInferenceQuery struct {
	inferenceApplication.InferenceQueryServiceInterface
	answers []inferenceEntity.OnboardingModelQuestionnaireAnswer
	err     error
	request inferenceTypes.GetOnboardingModelQuestionnaireAnswer
}

func (query *tutorialResetInferenceQuery) GetOnboardingModelQuestionnaireAnswers(
	_ context.Context,
	data inferenceTypes.GetOnboardingModelQuestionnaireAnswer,
) ([]inferenceEntity.OnboardingModelQuestionnaireAnswer, error) {
	query.request = data
	return query.answers, query.err
}

type tutorialResetInferenceCommand struct {
	inferenceApplication.InferenceCommandServiceInterface
	removedIDs []string
	errByID    map[string]error
}

func (command *tutorialResetInferenceCommand) RemoveOnboardingModelQuestionnaireAnswer(_ context.Context, ID string) error {
	command.removedIDs = append(command.removedIDs, ID)
	return command.errByID[ID]
}

func TestResetTutorialDeletesAllQuestionnaireAnswersForAuthenticatedScope(t *testing.T) {
	tenantQuery := &tutorialResetTenantQuery{
		answers: []tenantEntity.OnboardingQuestionnaireAnswer{{ID: "pre-survey"}, {ID: "post-survey"}},
	}
	tenantCommand := &tutorialResetTenantCommand{errByID: map[string]error{}}
	inferenceQuery := &tutorialResetInferenceQuery{
		answers: []inferenceEntity.OnboardingModelQuestionnaireAnswer{{ID: "cathef"}, {ID: "echoprime"}},
	}
	inferenceCommand := &tutorialResetInferenceCommand{errByID: map[string]error{}}
	service := &UserCommandService{
		TenantQueryServiceInterface:      tenantQuery,
		TenantCommandServiceInterface:    tenantCommand,
		InferenceQueryServiceInterface:   inferenceQuery,
		InferenceCommandServiceInterface: inferenceCommand,
	}

	err := service.ResetTutorial(context.Background(), userTypes.ResetTutorial{
		TenantID: "tenant-a",
		UserID:   "user-a",
	})
	if err != nil {
		t.Fatalf("ResetTutorial() error = %v", err)
	}

	if tenantQuery.request.TenantID != "tenant-a" || tenantQuery.request.UserID != "user-a" {
		t.Fatalf("tenant questionnaire query was not scoped to the authenticated user: %+v", tenantQuery.request)
	}
	if inferenceQuery.request.TenantID != "tenant-a" || inferenceQuery.request.UserID != "user-a" || inferenceQuery.request.ModelID != nil {
		t.Fatalf("model questionnaire query was not scoped across all models for the authenticated user: %+v", inferenceQuery.request)
	}
	if !reflect.DeepEqual(tenantCommand.removedIDs, []string{"pre-survey", "post-survey"}) {
		t.Fatalf("removed tenant questionnaire IDs = %v", tenantCommand.removedIDs)
	}
	if !reflect.DeepEqual(inferenceCommand.removedIDs, []string{"cathef", "echoprime"}) {
		t.Fatalf("removed model questionnaire IDs = %v", inferenceCommand.removedIDs)
	}
}

func TestResetTutorialIsIdempotentWithoutStoredAnswers(t *testing.T) {
	missingRecord := errors.New(apiError.MissingRecord)
	tenantCommand := &tutorialResetTenantCommand{errByID: map[string]error{}}
	inferenceCommand := &tutorialResetInferenceCommand{errByID: map[string]error{}}
	service := &UserCommandService{
		TenantQueryServiceInterface:      &tutorialResetTenantQuery{err: missingRecord},
		TenantCommandServiceInterface:    tenantCommand,
		InferenceQueryServiceInterface:   &tutorialResetInferenceQuery{err: missingRecord},
		InferenceCommandServiceInterface: inferenceCommand,
	}

	if err := service.ResetTutorial(context.Background(), userTypes.ResetTutorial{TenantID: "tenant-a", UserID: "user-a"}); err != nil {
		t.Fatalf("ResetTutorial() error = %v", err)
	}
	if len(tenantCommand.removedIDs) != 0 || len(inferenceCommand.removedIDs) != 0 {
		t.Fatalf("idempotent reset attempted deletes: tenant=%v model=%v", tenantCommand.removedIDs, inferenceCommand.removedIDs)
	}
}

func TestResetTutorialReturnsModelQuestionnaireDeleteFailure(t *testing.T) {
	deleteFailure := errors.New(apiError.FirestoreError)
	service := &UserCommandService{
		TenantQueryServiceInterface:   &tutorialResetTenantQuery{err: errors.New(apiError.MissingRecord)},
		TenantCommandServiceInterface: &tutorialResetTenantCommand{errByID: map[string]error{}},
		InferenceQueryServiceInterface: &tutorialResetInferenceQuery{
			answers: []inferenceEntity.OnboardingModelQuestionnaireAnswer{{ID: "cathef"}},
		},
		InferenceCommandServiceInterface: &tutorialResetInferenceCommand{
			errByID: map[string]error{"cathef": deleteFailure},
		},
	}

	err := service.ResetTutorial(context.Background(), userTypes.ResetTutorial{TenantID: "tenant-a", UserID: "user-a"})
	if !errors.Is(err, deleteFailure) {
		t.Fatalf("ResetTutorial() error = %v, want %v", err, deleteFailure)
	}
}
