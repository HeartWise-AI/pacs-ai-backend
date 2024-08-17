package service

import (
	"api-pacs/module/prediction/domain/repository"
)

// TenantCommandService handles the Tenant command service logic
type PredictionQuerryService struct {
	repository.PredictionQueryRepositoryInterface
}
