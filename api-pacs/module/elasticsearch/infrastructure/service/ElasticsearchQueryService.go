package service

import (
	"context"
	"encoding/json"
	"fmt"

	"api-pacs/module/elasticsearch/domain/repository"

	searchTypes "github.com/elastic/go-elasticsearch/v8/typedapi/types"
)

// ElasticsearchQueryService handles the Elasticsearch query service logic
type ElasticsearchQueryService struct {
	repository.ElasticsearchQueryRepositoryInterface
}

// SearchLoginLogs search login logs
func (service *ElasticsearchQueryService) SearchLoginLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) ([]map[string]interface{}, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, query)
	if err != nil {
		return nil, err
	}

	var mapData []map[string]interface{}

	for count, _ := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			fmt.Println(err)
		}

		var parsedMap map[string]interface{}

		err = json.Unmarshal([]byte(jsonData), &parsedMap)
		if err != nil {
			fmt.Println(err)
		}

		mapData = append(mapData, parsedMap)
	}

	return mapData, nil
}

// SearchAdminMemberLogs search admin member logs
func (service *ElasticsearchQueryService) SearchAdminMemberLogs(ctx context.Context, query map[string]searchTypes.MatchQuery) ([]map[string]interface{}, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, query)
	if err != nil {
		return nil, err
	}

	var mapData []map[string]interface{}

	for count, _ := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			fmt.Println(err)
		}

		var parsedMap map[string]interface{}

		err = json.Unmarshal([]byte(jsonData), &parsedMap)
		if err != nil {
			fmt.Println(err)
		}

		mapData = append(mapData, parsedMap)
	}

	return mapData, nil
}
