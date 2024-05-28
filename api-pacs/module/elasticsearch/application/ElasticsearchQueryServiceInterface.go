package application

import (
	"context"

	"api-pacs/infrastructures/database/elasticsearch/types"
	"api-pacs/module/elasticsearch/domain/entity"
)

// ElasticsearchQueryServiceInterface holds the implementable methods for the elasticsearch query service
type ElasticsearchQueryServiceInterface interface {
	SearchLoginLogs(ctx context.Context, searchParam types.SearchParameter) ([]entity.Login, error)
	SearchAdminMemberLogs(ctx context.Context, searchParam types.SearchParameter) ([]entity.AdminMember, error)
}
