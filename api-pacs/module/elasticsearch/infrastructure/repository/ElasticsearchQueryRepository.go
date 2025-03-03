package repository

import (
	"context"
	"errors"
	"log"

	"github.com/elastic/go-elasticsearch/v8/typedapi/cat/indices"
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

// GetAllIndices get all indices
func (repository *ElasticsearchQueryRepository) GetAllIndices() (indices.Response, error) {
	res, err := repository.ElasticsearchDBHandlerInterface.GetAllIndices()
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return res, nil
}

// SearchAdminMemberLogs searches admin member logs
func (repository *ElasticsearchQueryRepository) SearchAdminMemberLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	var adminMember entity.AdminMember

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocuments(ctx, types.SearchDocument{
		Index:     adminMember.GetModelName(),
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}

// SearchLoginLogs searches login logs
func (repository *ElasticsearchQueryRepository) SearchLoginLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	var login entity.Login

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocuments(ctx, types.SearchDocument{
		Index:     login.GetModelName(),
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}

// SearchModalityStudyLogs searches modality study logs
func (repository *ElasticsearchQueryRepository) SearchModalityStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	var modalityStudy entity.ModalityStudy

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocuments(ctx, types.SearchDocument{
		Index:     modalityStudy.GetModelName(),
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}

// SearchRetrievedStudyLogs searches retrieved study logs
func (repository *ElasticsearchQueryRepository) SearchRetrievedStudyLogs(ctx context.Context, data repositoryTypes.SearchDocument) (*search.Response, error) {
	var retrievedStudy entity.RetrievedStudy

	res, err := repository.ElasticsearchDBHandlerInterface.SearchDocuments(ctx, types.SearchDocument{
		Index:     retrievedStudy.GetModelName(),
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	if len(res.Hits.Hits) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}

	return res, nil
}
