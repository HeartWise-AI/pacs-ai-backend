package repository

import (
	"context"

	"api-pacs/module/inference/infrastructure/repository/types"
)

type InferenceCommandRepositoryInterface interface {
	// DeleteInferenceModel deletes an inference model
	DeleteInferenceModel(ctx context.Context, ID string) error
	// DeleteModelFeedback deletes model feedback
	DeleteModelFeedback(ctx context.Context, ID string) error
	// DeleteModelFeedbackAnswer deletes model feedback answer
	DeleteModelFeedbackAnswer(ctx context.Context, ID string) error
	// InsertModelFeedbackAnswer inserts a new  model feedback answer
	InsertModelFeedbackAnswer(ctx context.Context, data types.AddModelFeedbackAnswer) error
	// InsertInferenceModel inserts an inference model
	InsertInferenceModel(ctx context.Context, data types.AddInferenceModel) error
	// InsertOnboardingQuestionnaireAnswer inserts an onboarding questionnaire answer
	InsertOnboardingQuestionnaireAnswer(ctx context.Context, data types.AddOnboardingQuestionnaireAnswer) error
	// UpdateInferenceModel updates an inference model
	UpdateInferenceModel(ctx context.Context, data types.UpdateInferenceModel) error
	// UpdateInferenceModelContainerID updates the container ID of an inference model
	UpdateInferenceModelContainerID(ctx context.Context, ID, containerID string) error
	// UpsertModelFeedback upserts model feedback
	UpsertModelFeedback(ctx context.Context, data types.UpsertModelFeedback) error
}
