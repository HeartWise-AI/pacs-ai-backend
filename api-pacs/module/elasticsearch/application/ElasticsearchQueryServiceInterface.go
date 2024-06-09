package application

import (
	"context"

	"api-pacs/module/elasticsearch/domain/entity"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchQueryServiceInterface holds the implementable methods for the elasticsearch query service
type ElasticsearchQueryServiceInterface interface {
	SearchLoginLogs(ctx context.Context, data types.SearchDocument) ([]entity.Login, error)
	SearchAdminMemberLogs(ctx context.Context, data types.SearchDocument) ([]entity.AdminMember, error)
}
