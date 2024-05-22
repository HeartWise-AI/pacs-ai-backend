package elasticsearch

import (
	"context"
	"log"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"

	"api-pacs/infrastructures/database/elasticsearch/types"
)

// ElasticsearchDBHandler elasticsearch db handler
type ElasticsearchDBHandler struct {
	TypedClient *elasticsearch.TypedClient
}

// NewTypedClient create a new typed client for elasticsearch api
func NewTypedClient(config types.Config) (*ElasticsearchDBHandler, error) {
	typedclient, err := elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses: []string{config.ElasticsearchURL},
	})
	if err != nil {
		return nil, err
	}

	return &ElasticsearchDBHandler{
		TypedClient: typedclient,
	}, nil
}

// IndexDocument index a document
func (c *ElasticsearchDBHandler) IndexDocument(ctx context.Context, index string, document interface{}) (*index.Response, error) {
	res, err := c.TypedClient.Index(index).Request(document).Do(ctx)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// SearchDocument search a document
func (c *ElasticsearchDBHandler) SearchDocument(ctx context.Context, index string) (*search.Response, error) {
	res, err := c.TypedClient.Search().Index(index).Request(&search.Request{
		Query: &searchTypes.Query{
			Match: map[string]searchTypes.MatchQuery{
				"name": {Query: "Andrea"},
			},
		},
	}).Do(ctx)
	if err != nil {
		return nil, err
	}
	log.Print(res)

	return res, nil
}
