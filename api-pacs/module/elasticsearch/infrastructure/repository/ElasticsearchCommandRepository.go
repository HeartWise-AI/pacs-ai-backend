package repository

import (
	"api-pacs/infrastructures/database/redis/types"
)

// ElasticsearchCommandRepository handles elasticsearch command repository
type ElasticsearchCommandRepository struct {
	types.RedisDBHandlerInterface
}
