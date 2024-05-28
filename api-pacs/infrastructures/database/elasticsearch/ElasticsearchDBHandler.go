package elasticsearch

import (
	"context"
	"fmt"

	"github.com/elastic/go-elasticsearch/v8"
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
func (c *ElasticsearchDBHandler) SearchDocument(ctx context.Context, searchParam types.SearchParameter) (*search.Response, error) {
	res, err := c.TypedClient.Search().Index(searchParam.Index).Request(&search.Request{
		Query: &searchTypes.Query{
			Bool: &searchTypes.BoolQuery{
				Filter: []searchTypes.Query{
					{
						QueryString: &searchTypes.QueryStringQuery{
							Query: fmt.Sprintf("timestamp:[%v TO %v]", searchParam.StartDate, searchParam.EndDate),
						},
					},
				},
			},

			// 	Should: []searchTypes.Query{
			// 		{
			// 			Regexp: map[string]searchTypes.RegexpQuery{
			// 				"email": {
			// 					Value: "pacs",
			// 				},
			// 			},
			// 		},
			// 		// 		// 		{
			// 		// 		// 			Regexp: map[string]searchTypes.RegexpQuery{
			// 		// 		// 				"session_id": searchTypes.RegexpQuery{
			// 		// 		// 					Value: ".*XX7J.*",
			// 		// 		// 				},
			// 		// 		// 			},
			// 		// 		// 		},
			// 		// 		// 	},
			// 		// 		// },
			Regexp: map[string]searchTypes.RegexpQuery{
				"name": {
					Value: searchParam.Query,
				},
			},
			// MultiMatch: &searchTypes.MultiMatchQuery{
			// 	Query: searchParam.Query,
			// if fields is not provided will default search all fields on Multimatch query
			// },
			// Range: map[string]searchTypes.RangeQuery{
			// 	"@timestamp": map[string]interface{}{
			// 		"gte": searchParam.StartDate,
			// 		"lte": searchParam.EndDate,
			// 	},
			// },
			// QueryString: &searchTypes.QueryStringQuery{
			// 	Query: fmt.Sprintf("timestamp:[%v TO %v]", searchParam.StartDate, searchParam.EndDate),
			// },
		},
	}).Do(ctx)
	if err != nil {
		return nil, err
	}

	return res, nil
}
