package repository

import (
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func TestSelectModelFeedbackForExecutionKeepsOneVotePerReport(t *testing.T) {
	feedbacks := []entity.ModelFeedback{
		{ID: "legacy", ModelID: "model-a-1.0.0", UpdatedAt: 10},
		{ID: "report-1", ModelID: "model-a-1.0.0", ExecutionID: "execution-1", UpdatedAt: 20},
		{ID: "report-2", ModelID: "model-a-1.0.0", ExecutionID: "execution-2", UpdatedAt: 30},
	}

	selected, ok := selectModelFeedbackForExecution(feedbacks, "execution-1")
	require.True(t, ok)
	require.Equal(t, "report-1", selected.ID)

	selected, ok = selectModelFeedbackForExecution(feedbacks, " execution-2 ")
	require.True(t, ok)
	require.Equal(t, "report-2", selected.ID)

	// Without an execution id only the vote stored without a report matches.
	selected, ok = selectModelFeedbackForExecution(feedbacks, "")
	require.True(t, ok)
	require.Equal(t, "legacy", selected.ID)

	_, ok = selectModelFeedbackForExecution(feedbacks, "execution-3")
	require.False(t, ok)
	_, ok = selectModelFeedbackForExecution(feedbacks[1:], "")
	require.False(t, ok, "votes on reports never match a request without executionId")
	_, ok = selectModelFeedbackForExecution(nil, "")
	require.False(t, ok)
}

func TestSelectModelFeedbackForExecutionPicksNewestDuplicate(t *testing.T) {
	selected, ok := selectModelFeedbackForExecution([]entity.ModelFeedback{
		{ID: "b", ExecutionID: "execution-1", UpdatedAt: 20},
		{ID: "c", ExecutionID: "execution-1", UpdatedAt: 30},
		{ID: "a", ExecutionID: "execution-1", UpdatedAt: 30},
	}, "execution-1")
	require.True(t, ok)
	require.Equal(t, "a", selected.ID)
}
