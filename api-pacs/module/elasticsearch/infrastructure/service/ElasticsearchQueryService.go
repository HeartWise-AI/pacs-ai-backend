package service

import (
	"api-pacs/module/elasticsearch/domain/repository"
)

// ElasticsearchQueryService handles the Elasticsearch query service logic
type ElasticsearchQueryService struct {
	repository.ElasticsearchQueryRepositoryInterface
}
