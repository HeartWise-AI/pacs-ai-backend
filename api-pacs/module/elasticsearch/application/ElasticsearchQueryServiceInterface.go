package application

import (
	"context"

	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
)

// ElasticsearchQueryServiceInterface holds the implementable methods for the elasticsearch query service
type ElasticsearchQueryServiceInterface interface {
	SearchLoginLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) ([]map[string]interface{}, error)
	SearchAdminMemberLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) ([]map[string]interface{}, error)
}
