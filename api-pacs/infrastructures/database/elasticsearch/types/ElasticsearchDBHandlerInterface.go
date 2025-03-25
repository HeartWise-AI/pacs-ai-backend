package types

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/cat/indices"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
)

// ElasticsearchDBHandlerInterface list of implementable methods for elasticsearch db
type ElasticsearchDBHandlerInterface interface {
	// GetAllIndices get all indices
	GetAllIndices() (indices.Response, error)
	// IndexDocument index a document
	IndexDocument(ctx context.Context, index string, document interface{}) (*index.Response, error)
	// SearchDocuments searches documents
	SearchDocuments(ctx context.Context, param SearchDocument) (*search.Response, error)
}
