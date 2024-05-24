package repository

import (
	"context"
	"errors"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"
	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"

	"api-pacs/infrastructures/database/elasticsearch/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/domain/entity"
)

// ElasticsearchQueryRepository handles elasticsearch query repository
type ElasticsearchQueryRepository struct {
	types.ElasticsearchDBHandlerInterface
}

// SearchLoginLogs searches login logs
func (repository *ElasticsearchQueryRepository) SearchLoginLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) (*search.Response, error) {
	var login entity.Login

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocument(ctx, login.GetModelName(), query)
	if err != nil {
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}

// SearchAdminMemberLogs searches admin member logs
func (repository *ElasticsearchQueryRepository) SearchAdminMemberLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) (*search.Response, error) {
	var adminMember entity.AdminMember

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocument(ctx, adminMember.GetModelName(), query)
	if err != nil {
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}
