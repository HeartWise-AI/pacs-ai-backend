package repository

import (
	"context"
	"errors"
	"time"

	"github.com/elastic/go-elasticsearch/v8/typedapi/core/index"

	"api-pacs/infrastructures/database/elasticsearch/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/domain/entity"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

// ElasticsearchCommandRepository handles elasticsearch command repository
type ElasticsearchCommandRepository struct {
	types.ElasticsearchDBHandlerInterface
}

// InsertLoginLog insert login log
func (repository *ElasticsearchCommandRepository) InsertLoginLog(ctx context.Context, data repositoryTypes.CreateLoginLog) (*index.Response, error) {
	login := entity.Login{
		SessionID:  data.SessionID,
		TenantID:   data.TenantID,
		TenantName: data.TenantName,
		UserID:     data.UserID,
		Email:      data.Email,
		Name:       data.Name,
		Role:       data.Role,
		Specialty:  data.Specialty,
		Timestamp:  uint(time.Now().Unix()),
	}

	res, err := repository.ElasticsearchDBHandlerInterface.IndexDocument(ctx, login.GetModelName(), login)
	if err != nil {
		return nil, errors.New(apiError.DatabaseError)
	}

	return res, nil
}

// TODO: InsertAdminMemberLog
