package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	"api-pacs/module/elasticsearch/infrastructure/repository/types"
)

type ElasticsearchQueryRepositoryInterface interface {
	SearchLoginLogs(ctx context.Context, data types.SearchDocument) (*search.Response, error)
	SearchAdminMemberLogs(ctx context.Context, data types.SearchDocument) (*search.Response, error)
}
