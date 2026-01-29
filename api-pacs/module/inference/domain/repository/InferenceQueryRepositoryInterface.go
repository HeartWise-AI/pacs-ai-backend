package repository

import (
	"context"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/repository/types"
)

type InferenceQueryRepositoryInterface interface {
	// SelectInferenceModelByID get inference model by id
	SelectInferenceModelByID(ctx context.Context, ID string) (entity.InferenceModel, error)
	// SelectInferenceModelByContainer get inference model by container
	SelectInferenceModelByContainer(ctx context.Context, tenantID, containerID string) (entity.InferenceModel, error)
	// SelectInferenceModels get inference models by tenant id
	SelectInferenceModels(ctx context.Context, tenantID string) ([]entity.InferenceModel, error)
	// SelectModelFeedbackByUser get model feedback by model ID
	SelectModelFeedbackByUser(ctx context.Context, data types.GetModelFeedbackByUser) (entity.ModelFeedback, error)
	// SelectModelFeedbackAnswersByFeedbackID get model feedback answers by feedback ID
	SelectModelFeedbackAnswersByFeedbackID(ctx context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error)
}
