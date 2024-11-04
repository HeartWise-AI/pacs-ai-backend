package repository

import (
	"api-pacs/module/inference/domain/repository"
)

// InferenceQueryRepositoryCircuitBreaker is the circuit breaker for the inference query repository
type InferenceQueryRepositoryCircuitBreaker struct {
	repository.InferenceQueryRepositoryInterface
}
