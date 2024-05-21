package repository

import (
	"api-pacs/infrastructures/database/redis/types"
)

// ElasticsearchQueryRepository handles elasticsearch query repository
type ElasticsearchQueryRepository struct {
	types.RedisDBHandlerInterface
}
