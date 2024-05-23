package types

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
)

// ElasticsearchDBHandlerInterface list of implementable methods for elasticsearch db
type ElasticsearchDBHandlerInterface interface {
	IndexDocument(ctx context.Context, index string, document interface{}) (*index.Response, error)
	SearchDocument(ctx context.Context, index string, query map[string]searchTypes.MatchQuery) (*search.Response, error)
}
