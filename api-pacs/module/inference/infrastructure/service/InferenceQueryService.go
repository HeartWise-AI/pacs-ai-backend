package service

import (
	"api-pacs/module/inference/domain/repository"
)

// InferenceQueryService handles the Inference query service logic
type InferenceQueryService struct {
	repository.InferenceQueryRepositoryInterface
}
