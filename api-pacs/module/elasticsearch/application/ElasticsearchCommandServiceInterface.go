package application

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchCommandServiceInterface holds the implementable methods for the elasticsearch command service
type ElasticsearchCommandServiceInterface interface {
	CreateAdminMemberLog(ctx context.Context, data types.CreateAdminMemberLog) (*index.Response, error)
	CreateGetModalityStudyLog(ctx context.Context, data types.CreateGetModalityStudyLog) (*index.Response, error)
	CreateLoginLog(ctx context.Context, data types.CreateLoginLog) (*index.Response, error)
	CreateRetrieveStudyLog(ctx context.Context, data types.CreateRetrieveStudyLog) (*index.Response, error)
	SyncKibanaIndices(ctx context.Context) error
}
