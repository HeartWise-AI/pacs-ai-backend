package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

type ElasticsearchQueryRepositoryInterface interface {
	SearchAdminMemberLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	SearchLoginLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	SearchModalityStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	SearchRetrievedStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
}
