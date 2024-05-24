package rest

import (
	"api-pacs/module/elasticsearch/application"
)

// ElasticsearchCommandController request controller for elasticsearch command
type ElasticsearchCommandController struct {
	application.ElasticsearchCommandServiceInterface
}
