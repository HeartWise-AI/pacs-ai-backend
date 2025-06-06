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

// SearchAdminMemberLogs search admin member logs
func (service *ElasticsearchQueryService) SearchAdminMemberLogs(ctx context.Context, data types.SearchDocument) ([]entity.AdminMember, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchAdminMemberLogs(ctx, repositoryTypes.SearchDocument{
		TenantID:  data.TenantID,
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

// SearchLoginLogs search login logs
func (service *ElasticsearchQueryService) SearchLoginLogs(ctx context.Context, data types.SearchDocument) ([]entity.Login, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchLoginLogs(ctx, repositoryTypes.SearchDocument{
		TenantID:  data.TenantID,
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

// SearchModalityStudyLogs search modality study logs
func (service *ElasticsearchQueryService) SearchModalityStudyLogs(ctx context.Context, data types.SearchDocument) ([]entity.ModalityStudy, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchModalityStudyLogs(ctx, repositoryTypes.SearchDocument{
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	var modalityStudies []entity.ModalityStudy

	for count := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return nil, err
		}

		var modalityStudy entity.ModalityStudy
		err = json.Unmarshal([]byte(jsonData), &modalityStudy)
		if err != nil {
			return nil, err
		}

		modalityStudies = append(modalityStudies, modalityStudy)
	}

	return modalityStudies, nil
}

// SearchPredictInferenceModelLogs search predict inference model logs
func (service *ElasticsearchQueryService) SearchPredictInferenceModelLogs(ctx context.Context, data types.SearchDocument) ([]entity.PredictInferenceModel, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchPredictInferenceModelLogs(ctx, repositoryTypes.SearchDocument{
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	var predictInferenceModels []entity.PredictInferenceModel

	for count := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return nil, err
		}

		var predictInferenceModel entity.PredictInferenceModel
		err = json.Unmarshal([]byte(jsonData), &predictInferenceModel)
		if err != nil {
			return nil, err
		}

		predictInferenceModels = append(predictInferenceModels, predictInferenceModel)
	}

	return predictInferenceModels, nil
}

// SearchRetrievedStudyLogs search retrieved study logs
func (service *ElasticsearchQueryService) SearchRetrievedStudyLogs(ctx context.Context, data types.SearchDocument) ([]entity.RetrievedStudy, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchRetrievedStudyLogs(ctx, repositoryTypes.SearchDocument{
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	var retrievedStudies []entity.RetrievedStudy

	for count := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return nil, err
		}

		var retrievedStudy entity.RetrievedStudy
		err = json.Unmarshal([]byte(jsonData), &retrievedStudy)
		if err != nil {
			return nil, err
		}

		retrievedStudies = append(retrievedStudies, retrievedStudy)
	}

	return retrievedStudies, nil
}

// SearchStoredCustomSeriesLogs search stored custom series logs
func (service *ElasticsearchQueryService) SearchStoredCustomSeriesLogs(ctx context.Context, data types.SearchDocument) ([]entity.StoredCustomSeries, error) {
	res, err := service.ElasticsearchQueryRepositoryInterface.SearchStoredCustomSeriesLogs(ctx, repositoryTypes.SearchDocument{
		TenantID:  data.TenantID,
		Query:     data.Query,
		StartDate: data.StartDate,
		EndDate:   data.EndDate,
	})
	if err != nil {
		return nil, err
	}

	var storedCustomSeries []entity.StoredCustomSeries

	for count := range res.Hits.Hits {
		jsonData, err := json.Marshal(res.Hits.Hits[count].Source_)
		if err != nil {
			return nil, err
		}

		var customSeries entity.StoredCustomSeries
		err = json.Unmarshal([]byte(jsonData), &customSeries)
		if err != nil {
			return nil, err
		}
		storedCustomSeries = append(storedCustomSeries, customSeries)
	}

	return storedCustomSeries, nil
}
