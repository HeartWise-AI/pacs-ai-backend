package elasticsearch

import (
	elasticsearch "github.com/elastic/go-elasticsearch/v8"

	"api-pacs/infrastructures/providers/sdk/elasticsearch/types"
)

// ElasticSearchSDK elasticsearch sdk
type ElasticsearchSDK struct {
	TypedClient *elasticsearch.TypedClient
}

// NewTypedClient create a new typed client for elasticsearch api
func NewTypedClient(config types.Config) (*ElasticsearchSDK, error) {
	typedclient, err := elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses: []string{config.ElasticsearchURL},
	})
	if err != nil {
		return nil, err
	}

	return &ElasticsearchSDK{
		TypedClient: typedclient,
	}, nil
}
