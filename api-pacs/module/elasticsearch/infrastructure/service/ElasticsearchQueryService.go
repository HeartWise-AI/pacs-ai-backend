package service

import (
	"context"
	"encoding/json"
	"fmt"

	"api-pacs/infrastructures/database/elasticsearch/types"
	"api-pacs/module/elasticsearch/domain/entity"
	"api-pacs/module/elasticsearch/domain/repository"
)

// ElasticsearchQueryService handles the Elasticsearch query service logic
type ElasticsearchQueryService struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// SearchLoginLogs search login logs
func (service *ElasticsearchQueryService) SearchLoginLogs(ctx context.Context, searchParam types.SearchParameter) ([]entity.Login, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, searchParam)
	if err != nil {
		return nil, err
	}

	var logins []entity.Login

	for count, _ := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			fmt.Println(err)
		}

		var login entity.Login
		err = json.Unmarshal([]byte(jsonData), &login)
		if err != nil {
			fmt.Println(err)
		}

		logins = append(logins, login)
	}

	return logins, nil
}

// SearchAdminMemberLogs search admin member logs
func (service *ElasticsearchQueryService) SearchAdminMemberLogs(ctx context.Context, searchParam types.SearchParameter) ([]entity.AdminMember, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, searchParam)
	if err != nil {
		return nil, err
	}

	var adminMembers []entity.AdminMember

	for count, _ := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			fmt.Println(err)
		}

		var adminMember entity.AdminMember
		err = json.Unmarshal([]byte(jsonData), &adminMember)
		if err != nil {
			fmt.Println(err)
		}

		adminMembers = append(adminMembers, adminMember)
	}

	return adminMembers, nil
}
