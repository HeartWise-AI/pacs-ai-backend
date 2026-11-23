package service

import (
	"context"
	"errors"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// maximumFeedbackReportCountsSpan bounds the range of the report counts: the
// 12-month view and its prior period, with room to spare.
const maximumFeedbackReportCountsSpan = 800 * 24 * time.Hour

// ListFeedbackReportCounts returns the number of reports returned to the
// session tenant per model version and modality in [From, To), for workspace
// administrators. A report returned is a completed execution, dated by its
// last event (completed_at, else started_at).
func (service *InferencePredictionsService) ListFeedbackReportCounts(ctx context.Context, data types.ListFeedbackReportCounts) (types.FeedbackReportCounts, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	if tenantID == "" {
		return types.FeedbackReportCounts{}, errors.New(apiError.UnauthorizedAccess)
	}
	if !IsWorkspaceAdministrator(data.Role) {
		return types.FeedbackReportCounts{}, errors.New(apiError.ForbiddenAccess)
	}
	if data.From == nil || data.To == nil {
		return types.FeedbackReportCounts{}, errors.New(apiError.InvalidPayload)
	}
	from, to := data.From.UTC(), data.To.UTC()
	if !from.Before(to) || to.Sub(from) > maximumFeedbackReportCountsSpan {
		return types.FeedbackReportCounts{}, errors.New(apiError.InvalidPayload)
	}

	rows, err := service.Repository.CountReturnedReports(ctx, repositoryTypes.CountReturnedReports{
		TenantID: tenantID, From: from, To: to,
	})
	if err != nil {
		return types.FeedbackReportCounts{}, err
	}

	models := make([]types.FeedbackReportCountModel, 0, len(rows))
	for _, row := range rows {
		models = append(models, types.FeedbackReportCountModel{
			ModelName: row.ModelName, ModelVersion: row.ModelVersion,
			Modality: row.Modality, CompletedCount: row.CompletedCount,
		})
	}
	return types.FeedbackReportCounts{From: from, To: to, Models: models}, nil
}
