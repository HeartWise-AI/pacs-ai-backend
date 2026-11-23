package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// feedbackStore is an in-memory stand-in for the model_feedbacks and
// model_feedback_answers collections, keyed like the Firestore repository:
// one vote per (tenant, user, model version, execution).
type feedbackStore struct {
	domainRepository.InferenceQueryRepositoryInterface
	domainRepository.InferenceCommandRepositoryInterface
	feedbacks  map[string]entity.ModelFeedback
	answers    map[string]entity.ModelFeedbackAnswer
	selections []repositoryTypes.GetModelFeedbackByUserModelID
	nextAnswer int
}

func newFeedbackStore() *feedbackStore {
	return &feedbackStore{feedbacks: map[string]entity.ModelFeedback{}, answers: map[string]entity.ModelFeedbackAnswer{}}
}

func (store *feedbackStore) SelectModelFeedbackByUserModelID(_ context.Context, data repositoryTypes.GetModelFeedbackByUserModelID) (entity.ModelFeedback, error) {
	store.selections = append(store.selections, data)
	for _, feedback := range store.feedbacks {
		if feedback.TenantID == data.TenantID && feedback.UserID == data.UserID && feedback.ModelID == data.ModelID &&
			feedback.ExecutionID == strings.TrimSpace(data.ExecutionID) {
			return feedback, nil
		}
	}
	return entity.ModelFeedback{}, errors.New(apiError.MissingRecord)
}

func (store *feedbackStore) SelectModelFeedbackAnswersByFeedbackID(_ context.Context, feedbackID string) ([]entity.ModelFeedbackAnswer, error) {
	answers := []entity.ModelFeedbackAnswer{}
	for _, answer := range store.answers {
		if answer.ModelFeedbackID == feedbackID {
			answers = append(answers, answer)
		}
	}
	if len(answers) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}
	return answers, nil
}

func (store *feedbackStore) UpsertModelFeedback(_ context.Context, data repositoryTypes.UpsertModelFeedback) error {
	feedback, exists := store.feedbacks[data.ID]
	if !exists {
		feedback = entity.ModelFeedback{
			ID: data.ID, TenantID: data.TenantID, UserID: data.UserID,
			InferenceModelID: data.InferenceModelID, ModelID: data.ModelID,
		}
	}
	feedback.FeedbackType = data.FeedbackType
	feedback.RunID = data.RunID
	feedback.ExecutionID = data.ExecutionID
	feedback.StudyServiceJobID = data.StudyServiceJobID
	store.feedbacks[data.ID] = feedback
	return nil
}

func (store *feedbackStore) DeleteModelFeedback(_ context.Context, id string) error {
	delete(store.feedbacks, id)
	return nil
}

func (store *feedbackStore) InsertModelFeedbackAnswer(_ context.Context, data repositoryTypes.AddModelFeedbackAnswer) error {
	store.nextAnswer++
	id := fmt.Sprintf("answer-%d", store.nextAnswer)
	store.answers[id] = entity.ModelFeedbackAnswer{
		ID: id, ModelFeedbackID: data.ModelFeedbackID, QuestionnaireID: data.QuestionnaireID,
		QuestionnaireAnswerIDs: data.QuestionnaireAnswerIDs,
	}
	return nil
}

func (store *feedbackStore) DeleteModelFeedbackAnswer(_ context.Context, id string) error {
	delete(store.answers, id)
	return nil
}

func (store *feedbackStore) feedbackFor(executionID string) (entity.ModelFeedback, bool) {
	for _, feedback := range store.feedbacks {
		if feedback.ExecutionID == executionID {
			return feedback, true
		}
	}
	return entity.ModelFeedback{}, false
}

func feedbackStoreServices(store *feedbackStore) (*InferenceCommandService, *InferenceQueryService) {
	return &InferenceCommandService{
			InferenceQueryRepositoryInterface:   store,
			InferenceCommandRepositoryInterface: store,
		}, &InferenceQueryService{
			InferenceQueryRepositoryInterface: store,
		}
}

func reportVote(executionID string, feedbackType entity.FeedbackType) serviceTypes.UpdateModelFeedback {
	return serviceTypes.UpdateModelFeedback{
		TenantID: "tenant-a", UserID: "user-a", InferenceModelID: "inference-model-a",
		ModelID: "inference-model-a-1.0.0", FeedbackType: feedbackType,
		RunID: "run-" + executionID, ExecutionID: executionID,
	}
}

func TestTwoReportsOfTheSameModelGiveTwoVotes(t *testing.T) {
	store := newFeedbackStore()
	command, _ := feedbackStoreServices(store)

	require.NoError(t, command.UpdateModelFeedback(context.Background(), reportVote("execution-1", entity.ApproveFeedbackType)))
	require.NoError(t, command.UpdateModelFeedback(context.Background(), reportVote("execution-2", entity.RejectFeedbackType)))

	require.Len(t, store.feedbacks, 2)
	first, ok := store.feedbackFor("execution-1")
	require.True(t, ok)
	require.Equal(t, entity.ApproveFeedbackType, first.FeedbackType)
	second, ok := store.feedbackFor("execution-2")
	require.True(t, ok)
	require.Equal(t, entity.RejectFeedbackType, second.FeedbackType)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, "execution-1", store.selections[0].ExecutionID)
	require.Equal(t, "execution-2", store.selections[1].ExecutionID)
}

func TestSecondCreateWithoutIDUpdatesTheReportVote(t *testing.T) {
	store := newFeedbackStore()
	command, _ := feedbackStoreServices(store)

	require.NoError(t, command.UpdateModelFeedback(context.Background(), reportVote("execution-1", entity.ApproveFeedbackType)))
	created, _ := store.feedbackFor("execution-1")

	again := reportVote("execution-1", entity.RejectFeedbackType)
	again.ModelFeedbackAnswers = []serviceTypes.ModelFeedbackAnswer{{
		QuestionnaireID: "default-reject-reason", QuestionnaireAnswerIDs: []string{"missed-finding"},
	}}
	require.NoError(t, command.UpdateModelFeedback(context.Background(), again))
	require.NoError(t, command.UpdateModelFeedback(context.Background(), again))

	require.Len(t, store.feedbacks, 1, "no duplicate vote for the same report")
	updated, _ := store.feedbackFor("execution-1")
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, entity.RejectFeedbackType, updated.FeedbackType)
	require.Len(t, store.answers, 1, "answers are replaced, not accumulated")
	for _, answer := range store.answers {
		require.Equal(t, created.ID, answer.ModelFeedbackID)
		require.Equal(t, "default-reject-reason", answer.QuestionnaireID)
	}
}

func TestUpdateWithIDOfAnotherReportIsForbidden(t *testing.T) {
	store := newFeedbackStore()
	command, _ := feedbackStoreServices(store)
	require.NoError(t, command.UpdateModelFeedback(context.Background(), reportVote("execution-1", entity.ApproveFeedbackType)))
	first, _ := store.feedbackFor("execution-1")

	other := reportVote("execution-2", entity.RejectFeedbackType)
	other.ID = &first.ID
	require.EqualError(t, command.UpdateModelFeedback(context.Background(), other), apiError.ForbiddenAccess)

	same := reportVote("execution-1", entity.RejectFeedbackType)
	same.ID = &first.ID
	require.NoError(t, command.UpdateModelFeedback(context.Background(), same))
	require.Len(t, store.feedbacks, 1)
}

func TestGetAndRemoveAreScopedByExecution(t *testing.T) {
	store := newFeedbackStore()
	command, query := feedbackStoreServices(store)
	require.NoError(t, command.UpdateModelFeedback(context.Background(), reportVote("execution-1", entity.ApproveFeedbackType)))
	require.NoError(t, command.UpdateModelFeedback(context.Background(), reportVote("execution-2", entity.RejectFeedbackType)))

	got, err := query.GetModelFeedBackByUser(context.Background(), serviceTypes.GetModelFeedbackByUser{
		TenantID: "tenant-a", UserID: "user-a", ModelID: "inference-model-a-1.0.0", ExecutionID: "execution-2",
	})
	require.NoError(t, err)
	require.Equal(t, entity.RejectFeedbackType, got.FeedbackType)

	_, err = query.GetModelFeedBackByUser(context.Background(), serviceTypes.GetModelFeedbackByUser{
		TenantID: "tenant-a", UserID: "user-a", ModelID: "inference-model-a-1.0.0",
	})
	require.EqualError(t, err, apiError.MissingRecord, "without executionId only report-less votes match")

	require.NoError(t, command.RemoveModelFeedback(context.Background(), serviceTypes.RemoveModelFeedback{
		TenantID: "tenant-a", UserID: "user-a", ModelID: "inference-model-a-1.0.0", ExecutionID: "execution-1",
	}))
	require.Len(t, store.feedbacks, 1)
	_, ok := store.feedbackFor("execution-1")
	require.False(t, ok)
	_, ok = store.feedbackFor("execution-2")
	require.True(t, ok, "removing one report's vote leaves the other")
}

func TestVotesWithoutExecutionBehaveAsBefore(t *testing.T) {
	store := newFeedbackStore()
	command, query := feedbackStoreServices(store)
	legacy := reportVote("", entity.ApproveFeedbackType)
	legacy.RunID = ""

	require.NoError(t, command.UpdateModelFeedback(context.Background(), legacy))
	legacy.FeedbackType = entity.RejectFeedbackType
	require.NoError(t, command.UpdateModelFeedback(context.Background(), legacy))
	require.Len(t, store.feedbacks, 1)

	got, err := query.GetModelFeedBackByUser(context.Background(), serviceTypes.GetModelFeedbackByUser{
		TenantID: "tenant-a", UserID: "user-a", ModelID: "inference-model-a-1.0.0",
	})
	require.NoError(t, err)
	require.Equal(t, entity.RejectFeedbackType, got.FeedbackType)
	require.Equal(t, "", store.selections[0].ExecutionID)

	require.NoError(t, command.RemoveModelFeedback(context.Background(), serviceTypes.RemoveModelFeedback{
		TenantID: "tenant-a", UserID: "user-a", ModelID: "inference-model-a-1.0.0",
	}))
	require.Empty(t, store.feedbacks)
}
