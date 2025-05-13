package repository

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/cat/indices"
	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

type ElasticsearchQueryRepositoryInterface interface {
	// GetAllIndices get all indices
	GetAllIndices() (indices.Response, error)
	// SearchAdminMemberLogs searches admin member logs
	SearchAdminMemberLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	// SearchLoginLogs searches login logs
	SearchLoginLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	// SearchModalityStudyLogs searches modality study logs
	SearchModalityStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	// SearchPredictInferceModelLogs searches predict inference model logs
	SearchPredictInferenceModelLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
	// SearchRetrievedStudyLogs searches retrieved study logs
	SearchRetrievedStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error)
}
