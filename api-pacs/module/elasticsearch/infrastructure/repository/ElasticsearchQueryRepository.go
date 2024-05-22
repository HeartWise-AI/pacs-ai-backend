package repository

import (
	"api-pacs/infrastructures/database/elasticsearch/types"
)

// ElasticsearchQueryRepository handles elasticsearch query repository
type ElasticsearchQueryRepository struct {
	types.ElasticsearchDBHandlerInterface
}
