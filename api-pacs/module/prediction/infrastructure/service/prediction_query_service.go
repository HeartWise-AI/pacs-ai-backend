package service

import (
	"api-pacs/module/prediction/infrastructure/repository"
	"api-pacs/module/prediction/types"
	"context"
)

type PredictionQueryServiceInterface interface {
	GetPrediction(ctx context.Context, id string) (types.DicomPrediction, error)
}

type PredictionQueryService struct {
	PredictionQueryRepositoryInterface repository.PredictionQueryRepositoryInterface
}

func (s *PredictionQueryService) GetPrediction(ctx context.Context, id string) (types.DicomPrediction, error) {
	return s.PredictionQueryRepositoryInterface.GetPrediction(ctx, id)
}
