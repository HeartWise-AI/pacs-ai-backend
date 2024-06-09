package repository

import (
	"context"
	"errors"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/search"

	"api-pacs/infrastructures/database/elasticsearch/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/domain/entity"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

// ElasticsearchQueryRepository handles elasticsearch query repository
type ElasticsearchQueryRepository struct {
	types.ElasticsearchDBHandlerInterface
}

// SearchLoginLogs searches login logs
func (repository *ElasticsearchQueryRepository) SearchLoginLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	var login entity.Login

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocuments(ctx, types.SearchDocument{
		Index:     login.GetModelName(),
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}

// SearchAdminMemberLogs searches admin member logs
func (repository *ElasticsearchQueryRepository) SearchAdminMemberLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	var adminMember entity.AdminMember

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocuments(ctx, types.SearchDocument{
		Index:     adminMember.GetModelName(),
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}
