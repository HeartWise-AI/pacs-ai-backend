package types

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
)

// ElasticsearchDBHandlerInterface list of implementable methods for elasticsearch db
type ElasticsearchDBHandlerInterface interface {
	IndexDocument(ctx context.Context, index string, document interface{}) (*index.Response, error)
}
