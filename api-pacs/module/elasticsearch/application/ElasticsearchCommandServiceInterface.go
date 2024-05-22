package application

import (
	"context"

	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchCommandServiceInterface holds the implementable methods for the elasticsearch command service
type ElasticsearchCommandServiceInterface interface {
	CreateLoginLog(ctx context.Context, data types.CreateLoginLog) error
	CreateAdminMemberLog(ctx context.Context, data types.CreateAdminMemberLog) error
}
