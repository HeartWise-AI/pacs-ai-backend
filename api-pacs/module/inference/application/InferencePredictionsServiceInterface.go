package application

import (
	"context"
	"io"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// InferencePredictionsServiceInterface serves the AI Predictions page.
type InferencePredictionsServiceInterface interface {
	// ListPredictions returns one page of exams of a view; "all" needs ADMIN or OWNER.
	ListPredictions(ctx context.Context, data serviceTypes.ListPredictions) (serviceTypes.PredictionsPage, error)
	// GetPredictionModelHistory pages the executions of one model on one exam.
	GetPredictionModelHistory(ctx context.Context, data serviceTypes.GetPredictionModelHistory) (serviceTypes.PredictionHistoryPage, error)
	// ExportPredictions writes the CSV export; start is called once the request is accepted.
	ExportPredictions(ctx context.Context, data serviceTypes.ListPredictions, start func(truncated bool) io.Writer) (serviceTypes.PredictionsExportSummary, error)
}
