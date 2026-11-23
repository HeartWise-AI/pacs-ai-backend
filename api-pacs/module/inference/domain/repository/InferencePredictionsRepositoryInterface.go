package repository

import (
	"context"

	"api-pacs/module/inference/infrastructure/repository/types"
)

// InferencePredictionsRepositoryInterface reads the AI Predictions page. Every
// method is scoped to one tenant.
type InferencePredictionsRepositoryInterface interface {
	// ListPredictionExams returns one page of exams, most recent activity first, and the matching total.
	ListPredictionExams(ctx context.Context, data types.ListPredictionExams) (types.PredictionExamPage, error)
	// ListPredictionExecutions returns the executions of a bounded set of exams.
	ListPredictionExecutions(ctx context.Context, data types.ListPredictionExecutions) ([]types.PredictionExecutionRow, error)
	// ListPredictionModelHistory pages the executions of one model on one exam (limit+1 rows).
	ListPredictionModelHistory(ctx context.Context, data types.ListPredictionModelHistory) ([]types.PredictionExecutionRow, error)
	// ListPredictionLaunchers counts the runs of each exam by trigger and requester.
	ListPredictionLaunchers(ctx context.Context, tenantID string, studyInstanceUIDs []string) ([]types.PredictionLauncherRow, error)
	// CountReturnedReports counts the completed executions of each model
	// version and modality in a time range (Model Feedback denominator).
	CountReturnedReports(ctx context.Context, data types.CountReturnedReports) ([]types.ReturnedReportCountRow, error)
}
