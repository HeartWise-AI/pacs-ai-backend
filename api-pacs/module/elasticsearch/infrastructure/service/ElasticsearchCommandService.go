package service

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchCommandService handles the Elasticsearch command service logic
type ElasticsearchCommandService struct {
	repository.ElasticsearchCommandRepositoryInterface
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
