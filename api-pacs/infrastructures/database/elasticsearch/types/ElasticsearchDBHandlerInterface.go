package types

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
)

// ElasticsearchDBHandlerInterface list of implementable methods for elasticsearch db
type ElasticsearchDBHandlerInterface interface {
	IndexDocument(ctx context.Context, index string, document interface{}) (*index.Response, error)
	SearchDocuments(ctx context.Context, param SearchDocument) (*search.Response, error)
}
