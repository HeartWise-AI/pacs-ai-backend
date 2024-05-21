package repository

import (
	"api-pacs/module/elasticsearch/domain/repository"
)

// ElasticsearchQueryRepositoryCircuitBreaker is the circuit breaker for the elasticsearch query repository
type ElasticsearchQueryRepositoryCircuitBreaker struct {
	repository.ElasticsearchQueryRepositoryInterface
}
