package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

type ElasticsearchCommandRepositoryInterface interface {
	InsertAdminMemberLog(ctx context.Context, data repositoryTypes.CreateAdminMemberLog) (*index.Response, error)
	InsertGetModalityStudyLog(ctx context.Context, data repositoryTypes.CreateGetModalityStudyLog) (*index.Response, error)
	InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error)
	InsertRetrieveStudyLog(ctx context.Context, data repositoryTypes.CreateRetrieveStudyLog) (*index.Response, error)
}
