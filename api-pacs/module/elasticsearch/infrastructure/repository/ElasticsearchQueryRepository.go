package repository

import (
	"context"
	"errors"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	"api-pacs/infrastructures/database/elasticsearch/types"
	apiError "api-pacs/internal/errors"
)

// ElasticsearchQueryRepository handles elasticsearch query repository
type ElasticsearchQueryRepository struct {
	types.ElasticsearchDBHandlerInterface
}

// SearchLoginLogs searches login logs
func (repository *ElasticsearchQueryRepository) SearchLoginLogs(ctx context.Context, searchParam types.SearchParameter) (*search.Response, error) {
	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocument(ctx, searchParam)
	if err != nil {
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}

// SearchAdminMemberLogs searches admin member logs
func (repository *ElasticsearchQueryRepository) SearchAdminMemberLogs(ctx context.Context, searchParam types.SearchParameter) (*search.Response, error) {
	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocument(ctx, searchParam)
	if err != nil {
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}
