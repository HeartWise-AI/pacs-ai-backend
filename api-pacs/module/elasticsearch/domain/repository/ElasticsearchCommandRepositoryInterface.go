package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

type ElasticsearchCommandRepositoryInterface interface {
	InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error)
	InsertAdminMemberLog(ctx context.Context, data repositoryTypes.CreateAdminMemberLog) (*index.Response, error)
}
