package service

import (
	"context"
	"encoding/json"
	"errors"

	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/domain/entity"
	"api-pacs/module/elasticsearch/domain/repository"
)

// ElasticsearchQueryService handles the Elasticsearch query service logic
type ElasticsearchQueryService struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// SearchLoginLogs search login logs
func (service *ElasticsearchQueryService) SearchLoginLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) ([]entity.Login, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, query)
	if err != nil {
		if err == errors.New(apiError.MissingRecord) {
			return []entity.Login{}, err
		}

		return nil, err
	}

	var logins []entity.Login

	for count, _ := range res.Hits.Hits {
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
func (service *ElasticsearchQueryService) SearchAdminMemberLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) ([]entity.AdminMember, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, query)
	if err != nil {
		if err == errors.New(apiError.MissingRecord) {
			return []entity.AdminMember{}, err
		}

		return nil, err
	}

	var adminMembers []entity.AdminMember

	for count, _ := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return []entity.AdminMember{}, err
		}

		var adminMember entity.AdminMember
		err = json.Unmarshal([]byte(jsonData), &adminMember)
		if err != nil {
			return []entity.AdminMember{}, err
		}

		adminMembers = append(adminMembers, adminMember)
	}

	return adminMembers, nil
}
