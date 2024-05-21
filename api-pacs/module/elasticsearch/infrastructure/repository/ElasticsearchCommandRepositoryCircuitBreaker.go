package repository

import (
	hystrix_config "api-pacs/configs/hystrix"
	"api-pacs/module/elasticsearch/domain/repository"
)

// ElasticsearchCommandRepositoryCircuitBreaker circuit breaker for elasticsearch command repository
type ElasticsearchCommandRepositoryCircuitBreaker struct {
	repository.ElasticsearchCommandRepositoryInterface
}

var config = hystrix_config.Config{}
