package repository

import (
	"context"

	repositoryTypes "api-pacs/module/prediction/infrastructure/repository/types"
)

type PredictionCommandRepositoryInterface interface {
	SavePrediction(ctx context.Context, prediction repositoryTypes.DicomPrediction) error
}
