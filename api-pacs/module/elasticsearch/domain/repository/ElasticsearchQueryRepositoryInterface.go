package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	"api-pacs/infrastructures/database/elasticsearch/types"
)

type ElasticsearchQueryRepositoryInterface interface {
	SearchLoginLogs(ctx context.Context, searchParam types.SearchParameter) (*search.Response, error)
	SearchAdminMemberLogs(ctx context.Context, searchParam types.SearchParameter) (*search.Response, error)
}
