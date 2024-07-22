package repository

import (
	"api-pacs/module/prediction/types"
	"context"
)

type PredictionCommandRepositoryInterface interface {
	SavePrediction(ctx context.Context, prediction types.DicomPrediction) error
}

type PredictionCommandRepository struct {
	// Add any necessary dependencies
}

func (r *PredictionCommandRepository) SavePrediction(ctx context.Context, prediction types.DicomPrediction) error {
	// Implement the logic to save a prediction
	return nil
}

// PredictionCommandRepositoryCircuitBreaker for implementing circuit breaker pattern
type PredictionCommandRepositoryCircuitBreaker struct {
	PredictionCommandRepositoryInterface
}

func (cb *PredictionCommandRepositoryCircuitBreaker) SavePrediction(ctx context.Context, prediction types.DicomPrediction) error {
	// Implement circuit breaker logic here
	return cb.PredictionCommandRepositoryInterface.SavePrediction(ctx, prediction)
}
