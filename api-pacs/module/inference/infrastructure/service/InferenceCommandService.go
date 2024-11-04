package service

import (
	"api-pacs/module/inference/domain/repository"
)

// InferenceCommandService handles the Inference command service logic
type InferenceCommandService struct {
	repository.InferenceCommandRepositoryInterface
}
