package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type feedbackUpdateQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	feedback      entity.ModelFeedback
	feedbackErr   error
	answers       []entity.ModelFeedbackAnswer
	selectionData repositoryTypes.GetModelFeedbackByUserModelID
}

func (repository *feedbackUpdateQueryRepository) SelectModelFeedbackByUserModelID(_ context.Context, data repositoryTypes.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error) {
	repository.selectionData = data
	return repository.feedback, repository.feedbackErr
}

func (repository *feedbackUpdateQueryRepository) SelectModelFeedbackAnswersByFeedbackID(_ context.Context, _ string) ([]entity.ModelFeedbackAnswer, error) {
	return repository.answers, nil
}

type feedbackUpdateCommandRepository struct {
	domainRepository.InferenceCommandRepositoryInterface
	upserts []repositoryTypes.UpsertModelFeedback
	deletes []string
	inserts []repositoryTypes.AddModelFeedbackAnswer
}

func (repository *feedbackUpdateCommandRepository) UpsertModelFeedback(_ context.Context, data repositoryTypes.UpsertModelFeedback) error {
	repository.upserts = append(repository.upserts, data)
	return nil
}

func (repository *feedbackUpdateCommandRepository) DeleteModelFeedbackAnswer(_ context.Context, id string) error {
	repository.deletes = append(repository.deletes, id)
	return nil
}

func (repository *feedbackUpdateCommandRepository) InsertModelFeedbackAnswer(_ context.Context, data repositoryTypes.AddModelFeedbackAnswer) error {
	repository.inserts = append(repository.inserts, data)
	return nil
}

func TestUpdateModelFeedbackRejectsFeedbackOwnedByAnotherUser(t *testing.T) {
	requestedID := "feedback-other-user"
	queryRepository := &feedbackUpdateQueryRepository{
		feedback: entity.ModelFeedback{
			ID:               "feedback-current-user",
			TenantID:         "tenant-a",
			UserID:           "user-a",
			InferenceModelID: "inference-model-a",
			ModelID:          "model-a",
		},
	}
	commandRepository := &feedbackUpdateCommandRepository{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:   queryRepository,
		InferenceCommandRepositoryInterface: commandRepository,
	}

	err := service.UpdateModelFeedback(context.Background(), serviceTypes.UpdateModelFeedback{
		ID:               &requestedID,
		TenantID:         "tenant-a",
		UserID:           "user-a",
		InferenceModelID: "inference-model-a",
		ModelID:          "model-a",
		FeedbackType:     entity.ApproveFeedbackType,
	})

	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.Empty(t, commandRepository.upserts)
	require.Empty(t, commandRepository.deletes)
	require.Empty(t, commandRepository.inserts)
}

func TestUpdateModelFeedbackRejectsUnknownExistingFeedbackID(t *testing.T) {
	requestedID := "feedback-unknown"
	queryRepository := &feedbackUpdateQueryRepository{feedbackErr: errors.New(apiError.MissingRecord)}
	commandRepository := &feedbackUpdateCommandRepository{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:   queryRepository,
		InferenceCommandRepositoryInterface: commandRepository,
	}

	err := service.UpdateModelFeedback(context.Background(), serviceTypes.UpdateModelFeedback{
		ID:               &requestedID,
		TenantID:         "tenant-a",
		UserID:           "user-a",
		InferenceModelID: "inference-model-a",
		ModelID:          "model-a",
		FeedbackType:     entity.ApproveFeedbackType,
	})

	require.EqualError(t, err, apiError.ForbiddenAccess)
	require.Empty(t, commandRepository.upserts)
}

func TestUpdateModelFeedbackUpdatesOwnedFeedback(t *testing.T) {
	requestedID := "feedback-current-user"
	queryRepository := &feedbackUpdateQueryRepository{
		feedback: entity.ModelFeedback{
			ID:               requestedID,
			TenantID:         "tenant-a",
			UserID:           "user-a",
			InferenceModelID: "inference-model-a",
			ModelID:          "model-a",
		},
		answers: []entity.ModelFeedbackAnswer{{ID: "old-answer"}},
	}
	commandRepository := &feedbackUpdateCommandRepository{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:   queryRepository,
		InferenceCommandRepositoryInterface: commandRepository,
	}

	err := service.UpdateModelFeedback(context.Background(), serviceTypes.UpdateModelFeedback{
		ID:               &requestedID,
		TenantID:         "tenant-a",
		UserID:           "user-a",
		InferenceModelID: "inference-model-a",
		ModelID:          "model-a",
		FeedbackType:     entity.RejectFeedbackType,
		ModelFeedbackAnswers: []serviceTypes.ModelFeedbackAnswer{{
			QuestionnaireID:       "questionnaire-a",
			QuestionnaireQuestion: "Was the prediction correct?",
		}},
	})

	require.NoError(t, err)
	require.Equal(t, repositoryTypes.GetModelFeedbackByUserModelID{
		TenantID: "tenant-a",
		UserID:   "user-a",
		ModelID:  "model-a",
	}, queryRepository.selectionData)
	require.Len(t, commandRepository.upserts, 1)
	require.Equal(t, requestedID, commandRepository.upserts[0].ID)
	require.Equal(t, []string{"old-answer"}, commandRepository.deletes)
	require.Len(t, commandRepository.inserts, 1)
	require.Equal(t, requestedID, commandRepository.inserts[0].ModelFeedbackID)
}
