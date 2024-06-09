package service

import (
	"context"
	"encoding/json"

	"api-pacs/module/elasticsearch/domain/entity"
	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchQueryService handles the Elasticsearch query service logic
type ElasticsearchQueryService struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// SearchLoginLogs search login logs
func (service *ElasticsearchQueryService) SearchLoginLogs(ctx context.Context, data types.SearchDocument) ([]entity.Login, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, repositoryTypes.SearchDocument{
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	var logins []entity.Login

	for count := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return nil, err
		}

		var login entity.Login
		err = json.Unmarshal([]byte(jsonData), &login)
		if err != nil {
			return nil, err
		}

		logins = append(logins, login)
	}

	return logins, nil
}

// SearchAdminMemberLogs search admin member logs
func (service *ElasticsearchQueryService) SearchAdminMemberLogs(ctx context.Context, data types.SearchDocument) ([]entity.AdminMember, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, repositoryTypes.SearchDocument{
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	var adminMembers []entity.AdminMember

	for count := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return nil, err
		}

		var adminMember entity.AdminMember
		err = json.Unmarshal([]byte(jsonData), &adminMember)
		if err != nil {
			return nil, err
		}

		adminMembers = append(adminMembers, adminMember)
	}

	return adminMembers, nil
}
