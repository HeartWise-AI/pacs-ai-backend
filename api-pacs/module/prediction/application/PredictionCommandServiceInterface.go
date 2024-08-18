package application

import (
	"context"

	"api-pacs/module/prediction/infrastructure/service/types"
)

// TenantCommandServiceInterface holds the implementable methods for the tenant command service
type PredictionCommandServiceInterface interface {
	Predict(ctx context.Context, queryID string) (types.PredictionResult, error)
}
