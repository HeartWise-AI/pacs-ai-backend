package service

import (
	"context"
	"errors"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	kibanaAPITypes "api-pacs/infrastructures/providers/api/kibana/types"
	apiError "api-pacs/internal/errors"
	elasticsearchApplication "api-pacs/module/elasticsearch/application"
	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchCommandService handles the Elasticsearch command service logic
type ElasticsearchCommandService struct {
	repository.ElasticsearchCommandRepositoryInterface
	repository.ElasticsearchQueryRepositoryInterface
	elasticsearchApplication.ElasticsearchQueryServiceInterface
	kibanaAPITypes.KibanaAPIInterface
}

// SyncKibanaIndices sync kibana indices
func (service *ElasticsearchCommandService) SyncKibanaIndices(ctx context.Context) error {
	res, err := service.ElasticsearchQueryRepositoryInterface.GetAllIndices()
	if err != nil {
		return err
	}

	// loops all indices and creates data view on kibana, if existing it wont create a duplicate data view and returns an duplicate error message on log.
	for _, index := range res {
		err := service.KibanaAPIInterface.CreateDataView(ctx, kibanaAPITypes.DataView{
			Title: *index.Index,
			Name:  *index.Index,
		})
		if err != nil && err.Error() != apiError.KibanaDuplicateRecord {
			return errors.New(apiError.KibanaError)
		}
	}

	return nil
}

// CreateAdminMemberLog add a new admin member log
func (service *ElasticsearchCommandService) CreateAdminMemberLog(ctx context.Context, data types.CreateAdminMemberLog) (*index.Response, error) {
	res, err := service.ElasticsearchCommandRepositoryInterface.InsertAdminMemberLog(ctx, repositoryTypes.CreateAdminMemberLog{
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		Role:       data.Role,
		LicenseNo:  data.LicenseNo,
		Specialty:  data.Specialty,
		Action:     data.Action,
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

// CreateGetModalityStudyLog add a get modality study log
func (service *ElasticsearchCommandService) CreateGetModalityStudyLog(ctx context.Context, data types.CreateGetModalityStudyLog) (*index.Response, error) {
	res, err := service.ElasticsearchCommandRepositoryInterface.InsertGetModalityStudyLog(ctx, repositoryTypes.CreateGetModalityStudyLog{
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		ModalityID: data.ModalityID,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		QueryID:    data.QueryID,
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

// CreateLoginLog add a new login log
func (service *ElasticsearchCommandService) CreateLoginLog(ctx context.Context, data types.CreateLoginLog) (*index.Response, error) {
	res, err := service.ElasticsearchCommandRepositoryInterface.InsertLoginLog(ctx, repositoryTypes.CreateLoginLog{
		SessionID:  data.SessionID,
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		Role:       data.Role,
		Specialty:  data.Specialty,
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

// CreatePredictInferenceModelLog add a predict inference model log
func (service *ElasticsearchCommandService) CreatePredictInferenceModelLog(ctx context.Context, data types.CreatePredictInferenceModelLog) (*index.Response, error) {
	res, err := service.ElasticsearchCommandRepositoryInterface.InsertPredictInferenceModelLog(ctx, repositoryTypes.CreatePredictInferenceModelLog{
		TenantID:           data.TenantID,
		TenantName:         data.TenantName,
		ContainerID:        data.ContainerID,
		ContainerName:      data.ContainerName,
		InferenceModelID:   data.InferenceModelID,
		InferenceModelName: data.InferenceModelName,
		DockerImage:        data.DockerImage,
		StudyInstanceUID:   data.StudyInstanceUID,
		SeriesInstanceUIDs: data.SeriesInstanceUIDs,
		AdditionalMetadata: data.AdditionalMetadata,
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

// CreateRetrievedStudyLog add a retrieved study log
func (service *ElasticsearchCommandService) CreateRetrieveStudyLog(ctx context.Context, data types.CreateRetrieveStudyLog) (*index.Response, error) {
	res, err := service.ElasticsearchCommandRepositoryInterface.InsertRetrieveStudyLog(ctx, repositoryTypes.CreateRetrieveStudyLog{
		TenantID:         data.TenantID,
		TenantName:       data.TenantName,
		ModalityID:       data.ModalityID,
		UserID:           data.UserID,
		Email:            data.Email,
		Name:             data.Name,
		StudyInstanceUID: data.StudyInstanceUID,
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}
