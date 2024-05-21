package rest

import (
	"api-pacs/module/elasticsearch/application"
)

// ElasticsearchQueryController request controller for record query
type ElasticsearchQueryController struct {
	application.ElasticsearchQueryServiceInterface
}
