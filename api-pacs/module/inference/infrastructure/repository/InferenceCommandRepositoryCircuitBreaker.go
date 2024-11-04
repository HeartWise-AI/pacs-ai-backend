package repository

import (
	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/inference/domain/repository"
)

// InferenceCommandRepositoryCircuitBreaker circuit breaker for inference command repository
type InferenceCommandRepositoryCircuitBreaker struct {
	repository.InferenceCommandRepositoryInterface
}

var config = hystrix_config.Config{}
