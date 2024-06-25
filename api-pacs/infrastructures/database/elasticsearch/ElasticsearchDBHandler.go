package elasticsearch

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/cat/indices"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	ecsTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/sortorder"

	"api-pacs/infrastructures/database/elasticsearch/types"
)

// ElasticsearchDBHandler elasticsearch db handler
type ElasticsearchDBHandler struct {
	TypedClient *elasticsearch.TypedClient
}

// GetAllIndices get all indices from elasticsearch
func (c *ElasticsearchDBHandler) GetAllIndices() (indices.Response, error) {
	res, err := c.TypedClient.Cat.Indices().Do(context.Background())
	if err != nil {
		return nil, err
	}

	return res, nil
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

// SearchDocuments search ecs documents
func (c *ElasticsearchDBHandler) SearchDocuments(ctx context.Context, param types.SearchDocument) (*search.Response, error) {
	size := 5000 // TODO: max size, dynamic size for pagination
	startDateTimestamp := ecsTypes.Float64(param.StartDate)
	endtDateTimestamp := ecsTypes.Float64(param.EndDate)

	res, err := c.TypedClient.Search().Index(param.Index).Request(&search.Request{
		Query: &ecsTypes.Query{
			Bool: &ecsTypes.BoolQuery{
				Must: []ecsTypes.Query{
					{
						MultiMatch: &ecsTypes.MultiMatchQuery{
							Query:  param.TenantID,
							Fields: []string{"tenant_id"},
						},
					},
					{
						MultiMatch: &ecsTypes.MultiMatchQuery{
							Query:  param.Query,
							Fields: []string{"*"},
						},
					},
				},
				Filter: []ecsTypes.Query{
					{
						Range: map[string]ecsTypes.RangeQuery{
							"timestamp": ecsTypes.NumberRangeQuery{
								Gte: &startDateTimestamp,
								Lte: &endtDateTimestamp,
							},
						},
					},
				},
			},
		},
		Size: &size,
		Sort: []ecsTypes.SortCombinations{
			ecsTypes.SortOptions{
				SortOptions: map[string]ecsTypes.FieldSort{
					"timestamp": {
						Order: &sortorder.SortOrder{
							Name: "desc", // TODO: should be from params
						},
					},
				},
			},
		},
	}).Do(ctx)
	if err != nil {
		return nil, err
	}

	return res, nil
}
