package service

import (
	"context"

	"api-pacs/module/elasticsearch/domain/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
	"api-pacs/module/elasticsearch/infrastructure/service/types"
)

// ElasticsearchCommandService handles the Elasticsearch command service logic
type ElasticsearchCommandService struct {
	repository.ElasticsearchCommandRepositoryInterface
}

// CreateLoginLog add a new login log
func (service *ElasticsearchCommandService) CreateLoginLog(ctx context.Context, data types.CreateLoginLog) error {
	_, err := service.ElasticsearchCommandRepositoryInterface.InsertLoginLog(ctx, repositoryTypes.CreateLoginLog{
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
		return err
	}

	return nil
}

// CreateAdminMemberLog add a new admin member log
func (service *ElasticsearchCommandService) CreateAdminMemberLog(ctx context.Context, data types.CreateAdminMemberLog) error {
	_, err := service.ElasticsearchCommandRepositoryInterface.InsertAdminMemberLog(ctx, repositoryTypes.CreateAdminMemberLog{
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
		return err
	}

	return nil
}
