package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	userTypes "api-pacs/module/user/infrastructure/service/types"
)

type feedbackListQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	feedbacks       []entity.ModelFeedback
	answers         []entity.ModelFeedbackAnswer
	tenantIDs       []string
	answerIDs       [][]string
	feedbacksCalled int
}

func (repository *feedbackListQueryRepository) SelectModelFeedbacksByTenantID(_ context.Context, tenantID string) ([]entity.ModelFeedback, error) {
	repository.feedbacksCalled++
	repository.tenantIDs = append(repository.tenantIDs, tenantID)
	return repository.feedbacks, nil
}

func (repository *feedbackListQueryRepository) SelectModelFeedbackAnswersByFeedbackIDs(_ context.Context, ids []string) ([]entity.ModelFeedbackAnswer, error) {
	repository.answerIDs = append(repository.answerIDs, append([]string(nil), ids...))
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	answers := []entity.ModelFeedbackAnswer{}
	for _, answer := range repository.answers {
		if wanted[answer.ModelFeedbackID] {
			answers = append(answers, answer)
		}
	}
	return answers, nil
}

type feedbackListUsers struct {
	users []userTypes.GetTenantUser
	err   error
}

func (directory *feedbackListUsers) GetTenantUsers(context.Context, string) ([]userTypes.GetTenantUser, error) {
	return directory.users, directory.err
}

func feedbackListFixture() []entity.ModelFeedback {
	return []entity.ModelFeedback{
		{ID: "f1", TenantID: "tenant-a", UserID: "user-1", InferenceModelID: "deepcoro", ModelID: "deepcoro-2.2.0", FeedbackType: entity.ApproveFeedbackType, CreatedAt: 100, UpdatedAt: 1000, RunID: "run-1", ExecutionID: "exec-1", StudyServiceJobID: "job-1"},
		{ID: "f2", TenantID: "tenant-a", UserID: "user-2", InferenceModelID: "deepcoro", ModelID: "deepcoro-2.2.0", FeedbackType: entity.RejectFeedbackType, CreatedAt: 200, UpdatedAt: 3000},
		{ID: "f3", TenantID: "tenant-a", UserID: "user-1", InferenceModelID: "deepcoro", ModelID: "deepcoro-2.1.0", FeedbackType: entity.ApproveFeedbackType, CreatedAt: 300, UpdatedAt: 2000},
		{ID: "f4", TenantID: "tenant-a", UserID: "user-3", InferenceModelID: "echo", ModelID: "legacy-echo", FeedbackType: entity.RejectFeedbackType, CreatedAt: 400, UpdatedAt: 4000},
	}
}

func adminFeedbackRequest() serviceTypes.ListModelFeedback {
	return serviceTypes.ListModelFeedback{TenantID: "tenant-a", Role: iamEntity.AdminRole}
}

func TestListModelFeedbackForbidsNonAdministrators(t *testing.T) {
	repository := &feedbackListQueryRepository{feedbacks: feedbackListFixture()}
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	for _, role := range []string{iamEntity.UserRole, "", "SUPERUSER"} {
		_, err := service.ListModelFeedback(context.Background(), serviceTypes.ListModelFeedback{TenantID: "tenant-a", Role: role})
		require.EqualError(t, err, apiError.ForbiddenAccess, role)
	}
	require.Zero(t, repository.feedbacksCalled)
}

func TestListModelFeedbackUsesSessionTenantAndSummarizesPerVersion(t *testing.T) {
	repository := &feedbackListQueryRepository{feedbacks: feedbackListFixture()}
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	list, err := service.ListModelFeedback(context.Background(), serviceTypes.ListModelFeedback{TenantID: "tenant-a", Role: iamEntity.OwnerRole})

	require.NoError(t, err)
	require.Equal(t, []string{"tenant-a"}, repository.tenantIDs)
	require.Equal(t, []serviceTypes.ModelFeedbackSummary{
		{ModelID: "deepcoro-2.1.0", InferenceModelID: "deepcoro", Version: "2.1.0", ApproveCount: 1, LastUpdatedAt: 2000},
		{ModelID: "deepcoro-2.2.0", InferenceModelID: "deepcoro", Version: "2.2.0", ApproveCount: 1, RejectCount: 1, LastUpdatedAt: 3000},
		{ModelID: "legacy-echo", InferenceModelID: "echo", Version: "", RejectCount: 1, LastUpdatedAt: 4000},
	}, list.Summary)

	ids := []string{}
	for _, item := range list.Items {
		ids = append(ids, item.ID)
	}
	require.Equal(t, []string{"f4", "f2", "f3", "f1"}, ids, "newest first")
	require.Equal(t, "2.2.0", list.Items[1].ModelVersion)
	require.Equal(t, "", list.Items[0].ModelVersion, "legacy model id without version")
	require.False(t, list.Truncated)
}

func TestListModelFeedbackDropsOtherTenants(t *testing.T) {
	feedbacks := append(feedbackListFixture(), entity.ModelFeedback{ID: "other", TenantID: "tenant-b", ModelID: "deepcoro-2.2.0", InferenceModelID: "deepcoro", FeedbackType: entity.ApproveFeedbackType})
	repository := &feedbackListQueryRepository{feedbacks: feedbacks}
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())

	require.NoError(t, err)
	for _, item := range list.Items {
		require.NotEqual(t, "other", item.ID)
	}
	require.NotContains(t, repository.answerIDs[0], "other")
}

func TestListModelFeedbackFilters(t *testing.T) {
	from := time.Unix(1500, 0).UTC()
	to := time.Unix(3000, 0).UTC()

	for _, testCase := range []struct {
		name    string
		request serviceTypes.ListModelFeedback
		want    []string
	}{
		{name: "versioned model id", request: serviceTypes.ListModelFeedback{Model: "deepcoro-2.2.0"}, want: []string{"f2", "f1"}},
		{name: "inference model id", request: serviceTypes.ListModelFeedback{Model: "deepcoro"}, want: []string{"f2", "f3", "f1"}},
		{name: "date range is inclusive", request: serviceTypes.ListModelFeedback{From: &from, To: &to}, want: []string{"f2", "f3"}},
		{name: "approve only", request: serviceTypes.ListModelFeedback{FeedbackType: "APPROVE"}, want: []string{"f3", "f1"}},
		{name: "reject lower case", request: serviceTypes.ListModelFeedback{FeedbackType: "reject"}, want: []string{"f4", "f2"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := &InferenceQueryService{InferenceQueryRepositoryInterface: &feedbackListQueryRepository{feedbacks: feedbackListFixture()}}
			request := testCase.request
			request.TenantID, request.Role = "tenant-a", iamEntity.AdminRole

			list, err := service.ListModelFeedback(context.Background(), request)

			require.NoError(t, err)
			ids := []string{}
			for _, item := range list.Items {
				ids = append(ids, item.ID)
			}
			require.Equal(t, testCase.want, ids)
		})
	}
}

func TestListModelFeedbackRejectsInvalidQuery(t *testing.T) {
	from := time.Unix(2000, 0)
	to := time.Unix(1000, 0)
	for _, request := range []serviceTypes.ListModelFeedback{
		{FeedbackType: "MAYBE"},
		{From: &from, To: &to},
	} {
		repository := &feedbackListQueryRepository{feedbacks: feedbackListFixture()}
		service := &InferenceQueryService{InferenceQueryRepositoryInterface: repository}
		request.TenantID, request.Role = "tenant-a", iamEntity.AdminRole

		_, err := service.ListModelFeedback(context.Background(), request)

		require.EqualError(t, err, apiError.InvalidPayload)
		require.Zero(t, repository.feedbacksCalled)
	}
}

func TestListModelFeedbackAttachesAnswersToTheirItem(t *testing.T) {
	repository := &feedbackListQueryRepository{
		feedbacks: feedbackListFixture(),
		answers: []entity.ModelFeedbackAnswer{
			{ID: "a1", ModelFeedbackID: "f2", QuestionnaireID: "q1", QuestionnaireQuestion: "What was wrong?", QuestionnaireAnswerIDs: []string{"x", "y"}, QuestionnaireAnswers: []string{"Stenosis", "Side"}},
			{ID: "a2", ModelFeedbackID: "f2", QuestionnaireID: "q2", QuestionnaireQuestion: "Severity?"},
		},
	}
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())

	require.NoError(t, err)
	byID := map[string]serviceTypes.ModelFeedbackListItem{}
	for _, item := range list.Items {
		byID[item.ID] = item
	}
	require.Equal(t, []serviceTypes.ModelFeedbackListAnswer{
		{QuestionnaireID: "q1", QuestionnaireQuestion: "What was wrong?", QuestionnaireAnswerIDs: []string{"x", "y"}, QuestionnaireAnswers: []string{"Stenosis", "Side"}},
		{QuestionnaireID: "q2", QuestionnaireQuestion: "Severity?", QuestionnaireAnswerIDs: []string{}, QuestionnaireAnswers: []string{}},
	}, byID["f2"].Answers)
	require.Equal(t, []serviceTypes.ModelFeedbackListAnswer{}, byID["f1"].Answers)
	require.Equal(t, "run-1", byID["f1"].RunID)
	require.Equal(t, "exec-1", byID["f1"].ExecutionID)
	require.Equal(t, "job-1", byID["f1"].StudyServiceJobID)
}

func TestListModelFeedbackCapsItemsButCountsEverything(t *testing.T) {
	feedbacks := make([]entity.ModelFeedback, 0, maximumModelFeedbackItems+5)
	for index := range maximumModelFeedbackItems + 5 {
		feedbacks = append(feedbacks, entity.ModelFeedback{
			ID: fmt.Sprintf("f%05d", index), TenantID: "tenant-a", InferenceModelID: "m", ModelID: "m-1",
			FeedbackType: entity.ApproveFeedbackType, UpdatedAt: index,
		})
	}
	repository := &feedbackListQueryRepository{feedbacks: feedbacks}
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: repository}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())

	require.NoError(t, err)
	require.True(t, list.Truncated)
	require.Len(t, list.Items, maximumModelFeedbackItems)
	require.Equal(t, maximumModelFeedbackItems+4, list.Items[0].UpdatedAt)
	require.Len(t, repository.answerIDs[0], maximumModelFeedbackItems, "answers only for returned items")
	require.Equal(t, maximumModelFeedbackItems+5, list.Summary[0].ApproveCount)
}

func TestListModelFeedbackResolvesUserNames(t *testing.T) {
	repository := &feedbackListQueryRepository{feedbacks: feedbackListFixture()}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: repository,
		FeedbackUsers: &feedbackListUsers{users: []userTypes.GetTenantUser{
			{ID: "user-1", TenantID: "tenant-a", Name: "Ada", Email: "ada@example.org"},
			{ID: "user-2", TenantID: "tenant-b", Name: "Other tenant", Email: "other@example.org"},
			{ID: "user-3", TenantID: "tenant-a", Email: "noname@example.org"},
		}},
	}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())

	require.NoError(t, err)
	byID := map[string]serviceTypes.ModelFeedbackListItem{}
	for _, item := range list.Items {
		byID[item.ID] = item
	}
	require.Equal(t, "Ada", *byID["f1"].UserName)
	require.Equal(t, "ada@example.org", *byID["f1"].UserEmail)
	require.Nil(t, byID["f2"].UserName, "user of another tenant is ignored")
	require.Nil(t, byID["f4"].UserName)
	require.Equal(t, "noname@example.org", *byID["f4"].UserEmail)
}

func TestListModelFeedbackLeavesNamesNullWhenDirectoryFails(t *testing.T) {
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &feedbackListQueryRepository{feedbacks: feedbackListFixture()},
		FeedbackUsers:                     &feedbackListUsers{err: errors.New("directory down")},
	}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())

	require.NoError(t, err)
	for _, item := range list.Items {
		require.Nil(t, item.UserName)
		require.Nil(t, item.UserEmail)
	}
}

func TestListModelFeedbackSerialisesLegacyDocuments(t *testing.T) {
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: &feedbackListQueryRepository{feedbacks: []entity.ModelFeedback{
		{ID: "legacy", TenantID: "tenant-a", UserID: "user-1", InferenceModelID: "deepcoro", ModelID: "deepcoro-2.2.0", FeedbackType: entity.ApproveFeedbackType, CreatedAt: 10, UpdatedAt: 20},
	}}}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())
	require.NoError(t, err)

	encoded, err := json.Marshal(list)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"summary":[{"modelId":"deepcoro-2.2.0","inferenceModelId":"deepcoro","version":"2.2.0","approveCount":1,"rejectCount":0,"lastUpdatedAt":20}],
		"items":[{"id":"legacy","userId":"user-1","userName":null,"userEmail":null,"inferenceModelId":"deepcoro","modelId":"deepcoro-2.2.0","modelVersion":"2.2.0","feedbackType":"APPROVE","createdAt":10,"updatedAt":20,"runId":"","executionId":"","studyServiceJobId":"","answers":[]}],
		"truncated":false
	}`, string(encoded))
}

func TestListModelFeedbackEmptyTenant(t *testing.T) {
	service := &InferenceQueryService{InferenceQueryRepositoryInterface: &feedbackListQueryRepository{}}

	list, err := service.ListModelFeedback(context.Background(), adminFeedbackRequest())

	require.NoError(t, err)
	encoded, err := json.Marshal(list)
	require.NoError(t, err)
	require.JSONEq(t, `{"summary":[],"items":[],"truncated":false}`, string(encoded))
}
