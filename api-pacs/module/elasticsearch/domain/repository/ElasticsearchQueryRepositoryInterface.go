package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
)

type ElasticsearchQueryRepositoryInterface interface {
	SearchLoginLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) (*search.Response, error)
	SearchAdminMemberLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) (*search.Response, error)
}
