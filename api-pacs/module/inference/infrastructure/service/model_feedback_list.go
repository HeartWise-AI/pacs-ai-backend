package service

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/service/types"
)

// maximumModelFeedbackItems caps the votes returned with their answers; the
// summary always covers every matching vote.
const maximumModelFeedbackItems = 2000

// ListModelFeedback returns the model feedback of the session tenant for
// workspace administrators: counts per model version and the latest votes
// with their answers.
func (service *InferenceQueryService) ListModelFeedback(ctx context.Context, data types.ListModelFeedback) (types.ModelFeedbackList, error) {
	if !IsWorkspaceAdministrator(data.Role) {
		return types.ModelFeedbackList{}, errors.New(apiError.ForbiddenAccess)
	}

	tenantID := strings.TrimSpace(data.TenantID)
	if tenantID == "" {
		return types.ModelFeedbackList{}, errors.New(apiError.UnauthorizedAccess)
	}

	feedbackType := entity.FeedbackType(strings.ToUpper(strings.TrimSpace(data.FeedbackType)))
	if feedbackType != "" && feedbackType != entity.ApproveFeedbackType && feedbackType != entity.RejectFeedbackType {
		return types.ModelFeedbackList{}, errors.New(apiError.InvalidPayload)
	}
	if data.From != nil && data.To != nil && data.From.After(*data.To) {
		return types.ModelFeedbackList{}, errors.New(apiError.InvalidPayload)
	}

	feedbacks, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbacksByTenantID(ctx, tenantID)
	if err != nil {
		return types.ModelFeedbackList{}, err
	}

	model := strings.TrimSpace(data.Model)
	matched := make([]entity.ModelFeedback, 0, len(feedbacks))
	for _, feedback := range feedbacks {
		// Defence in depth: the query is already tenant scoped.
		if feedback.TenantID != tenantID {
			continue
		}
		if model != "" && feedback.ModelID != model && feedback.InferenceModelID != model {
			continue
		}
		if feedbackType != "" && feedback.FeedbackType != feedbackType {
			continue
		}
		if data.From != nil && int64(feedback.UpdatedAt) < data.From.Unix() {
			continue
		}
		if data.To != nil && int64(feedback.UpdatedAt) > data.To.Unix() {
			continue
		}
		matched = append(matched, feedback)
	}

	list := types.ModelFeedbackList{
		Summary: summarizeModelFeedback(matched),
		Items:   []types.ModelFeedbackListItem{},
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].UpdatedAt != matched[j].UpdatedAt {
			return matched[i].UpdatedAt > matched[j].UpdatedAt
		}
		return matched[i].ID < matched[j].ID
	})
	if len(matched) > maximumModelFeedbackItems {
		matched = matched[:maximumModelFeedbackItems]
		list.Truncated = true
	}
	if len(matched) == 0 {
		return list, nil
	}

	// Answers are loaded only for the returned, tenant-scoped feedback ids.
	ids := make([]string, 0, len(matched))
	for _, feedback := range matched {
		ids = append(ids, feedback.ID)
	}
	answers, err := service.InferenceQueryRepositoryInterface.SelectModelFeedbackAnswersByFeedbackIDs(ctx, ids)
	if err != nil {
		return types.ModelFeedbackList{}, err
	}
	answersByFeedback := map[string][]types.ModelFeedbackListAnswer{}
	for _, answer := range answers {
		answersByFeedback[answer.ModelFeedbackID] = append(answersByFeedback[answer.ModelFeedbackID], types.ModelFeedbackListAnswer{
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: nonNilStrings(answer.QuestionnaireAnswerIDs),
			QuestionnaireAnswers:   nonNilStrings(answer.QuestionnaireAnswers),
		})
	}

	users := service.feedbackUsers(ctx, tenantID)

	list.Items = make([]types.ModelFeedbackListItem, 0, len(matched))
	for _, feedback := range matched {
		item := types.ModelFeedbackListItem{
			ID:                feedback.ID,
			UserID:            feedback.UserID,
			InferenceModelID:  feedback.InferenceModelID,
			ModelID:           feedback.ModelID,
			ModelVersion:      modelFeedbackVersion(feedback),
			FeedbackType:      feedback.FeedbackType,
			CreatedAt:         feedback.CreatedAt,
			UpdatedAt:         feedback.UpdatedAt,
			RunID:             feedback.RunID,
			ExecutionID:       feedback.ExecutionID,
			StudyServiceJobID: feedback.StudyServiceJobID,
			Answers:           answersByFeedback[feedback.ID],
		}
		if item.Answers == nil {
			item.Answers = []types.ModelFeedbackListAnswer{}
		}
		if user, ok := users[strings.TrimSpace(feedback.UserID)]; ok {
			item.UserName = user.name
			item.UserEmail = user.email
		}
		list.Items = append(list.Items, item)
	}

	return list, nil
}

// modelFeedbackVersion derives the version from the legacy key
// "{inferenceModelID}-{version}"; a model id without that prefix has none.
func modelFeedbackVersion(feedback entity.ModelFeedback) string {
	prefix := feedback.InferenceModelID + "-"
	if feedback.InferenceModelID == "" || !strings.HasPrefix(feedback.ModelID, prefix) {
		return ""
	}
	return strings.TrimPrefix(feedback.ModelID, prefix)
}

func summarizeModelFeedback(feedbacks []entity.ModelFeedback) []types.ModelFeedbackSummary {
	byModel := map[string]*types.ModelFeedbackSummary{}
	for _, feedback := range feedbacks {
		summary, ok := byModel[feedback.ModelID]
		if !ok {
			summary = &types.ModelFeedbackSummary{
				ModelID:          feedback.ModelID,
				InferenceModelID: feedback.InferenceModelID,
				Version:          modelFeedbackVersion(feedback),
			}
			byModel[feedback.ModelID] = summary
		}
		switch feedback.FeedbackType {
		case entity.ApproveFeedbackType:
			summary.ApproveCount++
		case entity.RejectFeedbackType:
			summary.RejectCount++
		}
		if feedback.UpdatedAt > summary.LastUpdatedAt {
			summary.LastUpdatedAt = feedback.UpdatedAt
		}
	}

	summaries := make([]types.ModelFeedbackSummary, 0, len(byModel))
	for _, summary := range byModel {
		summaries = append(summaries, *summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ModelID < summaries[j].ModelID })
	return summaries
}

type feedbackUser struct {
	name  *string
	email *string
}

// feedbackUsers maps user ids of the tenant to their name and email. A
// missing directory or a failed lookup leaves every name and email null.
func (service *InferenceQueryService) feedbackUsers(ctx context.Context, tenantID string) map[string]feedbackUser {
	users := map[string]feedbackUser{}
	if service.FeedbackUsers == nil {
		return users
	}
	tenantUsers, err := service.FeedbackUsers.GetTenantUsers(ctx, tenantID)
	if err != nil {
		log.Printf("[model feedback] user lookup failed err=%v", err)
		return users
	}
	for _, user := range tenantUsers {
		if user.TenantID != "" && user.TenantID != tenantID {
			continue
		}
		var entry feedbackUser
		if name := strings.TrimSpace(user.Name); name != "" {
			entry.name = &name
		}
		if email := strings.TrimSpace(user.Email); email != "" {
			entry.email = &email
		}
		users[strings.TrimSpace(user.ID)] = entry
	}
	return users
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
